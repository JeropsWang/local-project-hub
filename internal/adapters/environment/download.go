package environment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path"

	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
)

const maxRuntimeDownload int64 = 1024 * 1024 * 1024

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

func (p *NativeRuntimeProvider) download(ctx context.Context, root *os.Root, artifact RuntimeArtifact) (string, error) {
	if err := root.MkdirAll("cache/downloads", 0700); err != nil {
		return "", err
	}
	cache := path.Join("cache/downloads", artifact.SHA256+"."+artifact.Format)
	if info, err := root.Lstat(cache); err == nil {
		if !info.Mode().IsRegular() || info.Size() > maxRuntimeDownload {
			return "", problem.New("ARTIFACT_CACHE_CORRUPT", "")
		}
		file, err := root.Open(cache)
		if err != nil {
			return "", err
		}
		sum := sha256.New()
		_, err = io.Copy(sum, contextReader{ctx, file})
		file.Close()
		if err != nil {
			return "", err
		}
		if hex.EncodeToString(sum.Sum(nil)) != artifact.SHA256 {
			return "", problem.New("ARTIFACT_CACHE_CORRUPT", "")
		}
		return cache, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	nonce, err := identity.NewUUID()
	if err != nil {
		return "", err
	}
	temporary := cache + ".partial-" + nonce
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		_ = root.Remove(temporary)
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return "", err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", problem.New("ARTIFACT_DOWNLOAD_FAILED", "")
	}
	if response.ContentLength > maxRuntimeDownload {
		return "", problem.New("ARTIFACT_DOWNLOAD_LIMIT", "")
	}
	sum := sha256.New()
	n, err := io.Copy(
		io.MultiWriter(file, sum),
		io.LimitReader(contextReader{ctx, response.Body}, maxRuntimeDownload+1),
	)
	if err != nil {
		return "", err
	}
	if n > maxRuntimeDownload {
		return "", problem.New("ARTIFACT_DOWNLOAD_LIMIT", "")
	}
	if hex.EncodeToString(sum.Sum(nil)) != artifact.SHA256 {
		return "", problem.New("ARTIFACT_HASH_MISMATCH", "")
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	closed = true
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := root.Lstat(cache); !os.IsNotExist(err) {
		return "", problem.New("ARTIFACT_CACHE_CONFLICT", "")
	}
	if err := root.Rename(temporary, cache); err != nil {
		return "", err
	}
	return cache, nil
}
