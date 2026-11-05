package environment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/preparation"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

const maxSourceBytes int64 = 4 * 1024 * 1024 * 1024
const maxSourceFile int64 = 256 * 1024 * 1024
const maxSourceEntries = 100000

// The clean copy deliberately rebuilds dependencies and excludes conventional local
// secrets/data. Arbitrary script dependencies still need explicit declarations.
func excludedSource(name string) bool {
	for _, part := range strings.Split(strings.ToLower(name), "/") {
		switch part {
		case ".git",
			".worktrees",
			".superpowers",
			".venv",
			".venv-image-match",
			".deps",
			"node_modules",
			"__pycache__",
			".pytest_cache",
			".mypy_cache",
			".cache",
			".next",
			"dist",
			"build",
			"target",
			"logs",
			"data",
			"backups",
			".npmrc",
			".pypirc",
			".netrc",
			".ssh":
			return true
		}
		if strings.HasPrefix(part, ".lph-") || part == ".env" ||
			strings.HasPrefix(part, ".env.") &&
				!slices.Contains([]string{".env.example", ".env.sample", ".env.template"}, part) {
			return true
		}
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".pem", ".key", ".pfx", ".p12", ".db", ".sqlite", ".sqlite3", ".pyc":
		return true
	}
	return false
}

func scanSource(ctx context.Context, binding project.Binding) ([]preparation.File, error) {
	anchor, err := filesystem.OpenLocalRoot(binding.LocalLocator)
	if err != nil {
		return nil, err
	}
	defer anchor.Close()
	files := []preparation.File{}
	var total int64
	entries := 0
	err = fs.WalkDir(anchor.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > maxSourceEntries {
			return problem.New("SOURCE_COPY_LIMIT", binding.Key)
		}
		if name == "." {
			return nil
		}
		if excludedSource(name) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if project.ValidateRelativePath(name) != nil {
			return problem.New("SOURCE_PATH_UNSUPPORTED", name)
		}
		info, err := anchor.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return problem.New("SOURCE_LINK_UNSUPPORTED", name)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() > maxSourceFile {
			return problem.New("SOURCE_COPY_LIMIT", name)
		}
		file, err := anchor.Open(name)
		if err != nil {
			return err
		}
		digest := sha256.New()
		n, err := io.Copy(digest, io.LimitReader(contextReader{ctx, file}, maxSourceFile+1))
		file.Close()
		if err != nil {
			return err
		}
		total += n
		if n != info.Size() || n > maxSourceFile || total > maxSourceBytes {
			return problem.New("SOURCE_COPY_LIMIT", name)
		}
		files = append(files, preparation.File{Path: name, Size: n, SHA256: hex.EncodeToString(digest.Sum(nil))})
		return nil
	})
	return files, err
}

func sourcePlanFingerprint(plan preparation.Plan) string {
	plan.Fingerprint = ""
	data, _ := json.Marshal(plan)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func copySource(ctx context.Context, destination *os.Root, relative string, source preparation.Source) error {
	anchor, err := filesystem.OpenLocalRoot(source.Binding.LocalLocator)
	if err != nil {
		return err
	}
	defer anchor.Close()
	if err := destination.MkdirAll(relative, 0700); err != nil {
		return err
	}
	for _, file := range source.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if excludedSource(file.Path) || project.ValidateRelativePath(file.Path) != nil {
			return problem.New("SOURCE_PLAN_STALE", "")
		}
		// Recheck every ancestor: os.Root enforces containment but permits internal
		// links; source copies reject those as well rather than silently following.
		for current := file.Path; current != "."; current = path.Dir(current) {
			info, err := anchor.Lstat(current)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return problem.New("SOURCE_PLAN_STALE", file.Path)
			}
		}
		input, err := anchor.Open(file.Path)
		if err != nil {
			return err
		}
		target := path.Join(relative, file.Path)
		if err := destination.MkdirAll(path.Dir(target), 0700); err != nil {
			input.Close()
			return err
		}
		output, err := destination.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			input.Close()
			return err
		}
		digest := sha256.New()
		n, copyErr := io.Copy(
			io.MultiWriter(output, digest),
			io.LimitReader(contextReader{ctx, input}, maxSourceFile+1),
		)
		input.Close()
		syncErr, closeErr := output.Sync(), output.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != file.Size || hex.EncodeToString(digest.Sum(nil)) != file.SHA256 {
			return problem.New("SOURCE_PLAN_STALE", file.Path)
		}
	}
	return nil
}
