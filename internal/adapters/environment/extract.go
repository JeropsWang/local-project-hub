package environment

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

const maxRuntimeExpanded int64 = 4 * 1024 * 1024 * 1024
const maxRuntimeFile int64 = 1024 * 1024 * 1024
const maxRuntimeEntries = 100000

type runtimeFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type runtimeExtractor struct {
	ctx      context.Context
	root     *os.Root
	stage    string
	artifact RuntimeArtifact
	total    int64
	entries  int
	seen     map[string]string
	files    []runtimeFile
}

func extractRuntime(
	ctx context.Context,
	root *os.Root,
	cache, stage string,
	artifact RuntimeArtifact,
) ([]runtimeFile, error) {
	extractor := &runtimeExtractor{
		ctx:      ctx,
		root:     root,
		stage:    stage,
		artifact: artifact,
		seen:     map[string]string{},
		files:    []runtimeFile{},
	}
	file, err := root.Open(cache)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	switch artifact.Format {
	case "zip":
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		reader, err := zip.NewReader(file, info.Size())
		if err != nil {
			return nil, problem.New("RUNTIME_ARCHIVE_INVALID", "")
		}
		if len(reader.File) > maxRuntimeEntries {
			return nil, problem.New("RUNTIME_ARCHIVE_LIMIT", "")
		}
		for _, entry := range reader.File {
			if entry.Flags&1 != 0 {
				return nil, problem.New("RUNTIME_ARCHIVE_ENCRYPTED", "")
			}
			mode := entry.Mode()
			if !mode.IsRegular() && !mode.IsDir() {
				return nil, problem.New("RUNTIME_ARCHIVE_LINK_REJECTED", "")
			}
			if mode.IsDir() {
				if err := extractor.entry(entry.Name, true, nil, 0); err != nil {
					return nil, err
				}
				continue
			}
			if entry.UncompressedSize64 > uint64(maxRuntimeFile) {
				return nil, problem.New("RUNTIME_ARCHIVE_LIMIT", "")
			}
			stream, err := entry.Open()
			if err != nil {
				return nil, err
			}
			err = extractor.entry(entry.Name, false, stream, int64(entry.UncompressedSize64))
			stream.Close()
			if err != nil {
				return nil, err
			}
		}
	case "tar.gz":
		compressed, err := gzip.NewReader(file)
		if err != nil {
			return nil, problem.New("RUNTIME_ARCHIVE_INVALID", "")
		}
		defer compressed.Close()
		budget := &runtimeBudgetReader{reader: contextReader{ctx, compressed}, remaining: maxRuntimeExpanded}
		reader := tar.NewReader(budget)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, problem.New("RUNTIME_ARCHIVE_INVALID", "")
			}
			switch header.Typeflag {
			case tar.TypeDir:
				err = extractor.entry(header.Name, true, nil, 0)
			case tar.TypeReg, tar.TypeRegA:
				err = extractor.entry(header.Name, false, reader, header.Size)
			default:
				return nil, problem.New("RUNTIME_ARCHIVE_LINK_REJECTED", "")
			}
			if err != nil {
				return nil, err
			}
		}
		// Read through the gzip trailer so a truncated or corrupt stream cannot publish.
		if _, err := io.Copy(io.Discard, budget); err != nil {
			return nil, problem.New("RUNTIME_ARCHIVE_INVALID", "")
		}
	default:
		return nil, problem.New("RUNTIME_ARCHIVE_FORMAT_UNSUPPORTED", "")
	}
	sort.Slice(extractor.files, func(i, j int) bool { return extractor.files[i].Path < extractor.files[j].Path })
	return extractor.files, nil
}

// runtimeBudgetReader includes tar headers, padding and trailing data in the budget.
type runtimeBudgetReader struct {
	reader    io.Reader
	remaining int64
}

func (r *runtimeBudgetReader) Read(data []byte) (int, error) {
	if int64(len(data)) > r.remaining+1 {
		data = data[:r.remaining+1]
	}
	n, err := r.reader.Read(data)
	r.remaining -= int64(n)
	if r.remaining < 0 {
		return n, problem.New("RUNTIME_ARCHIVE_LIMIT", "")
	}
	return n, err
}

func (e *runtimeExtractor) entry(name string, directory bool, reader io.Reader, size int64) error {
	if err := e.ctx.Err(); err != nil {
		return err
	}
	e.entries++
	if e.entries > maxRuntimeEntries {
		return problem.New("RUNTIME_ARCHIVE_LIMIT", "")
	}
	name = strings.TrimSuffix(name, "/")
	if err := project.ValidateRelativePath(name); err != nil {
		return err
	}
	prefix := strings.TrimSuffix(e.artifact.StripPrefix, "/")
	if name == prefix && directory {
		return nil
	}
	if !strings.HasPrefix(name, e.artifact.StripPrefix) {
		return problem.New("RUNTIME_ARCHIVE_PREFIX_MISMATCH", "")
	}
	relative := strings.TrimPrefix(name, e.artifact.StripPrefix)
	if err := project.ValidateRelativePath(relative); err != nil {
		return err
	}
	if relative == runtimeMarker {
		return problem.New("RUNTIME_ARCHIVE_RESERVED_PATH", "")
	}
	canonical := strings.ToUpper(relative)
	if original, ok := e.seen[canonical]; ok {
		if directory && original == relative+"/" {
			return nil
		}
		return problem.New("RUNTIME_ARCHIVE_PATH_CONFLICT", "")
	}
	if directory {
		e.seen[canonical] = relative + "/"
	} else {
		e.seen[canonical] = relative
	}
	dest := path.Join(e.stage, relative)
	if directory {
		return e.root.MkdirAll(dest, 0700)
	}
	if size < 0 || size > maxRuntimeFile || e.total+size > maxRuntimeExpanded {
		return problem.New("RUNTIME_ARCHIVE_LIMIT", "")
	}
	if err := e.root.MkdirAll(path.Dir(dest), 0700); err != nil {
		return err
	}
	output, err := e.root.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	digest := sha256.New()
	n, copyErr := io.Copy(
		io.MultiWriter(output, digest),
		io.LimitReader(contextReader{e.ctx, reader}, maxRuntimeFile+1),
	)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	e.total += n
	if n > maxRuntimeFile || e.total > maxRuntimeExpanded || n != size {
		return problem.New("RUNTIME_ARCHIVE_LIMIT", "")
	}
	e.files = append(e.files, runtimeFile{Path: relative, Size: n, SHA256: hex.EncodeToString(digest.Sum(nil))})
	return nil
}
