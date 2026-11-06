package application

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

const maxCoverBytes = 1 << 20

// Cover returns a small included raster. External file locators and active
// formats never reach a WebView image source.
func (s *ProjectService) Cover(ctx context.Context, id ProjectID) (string, error) {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo := s.workspaces.current
	if repo == nil {
		return "", problem.New("WORKSPACE_REQUIRED", "")
	}
	view, err := repo.Get(ctx, string(id))
	if err != nil {
		return "", err
	}
	if view.Document.Appearance == nil {
		return "", nil
	}
	file := view.Document.Files[view.Document.Appearance.CoverFileID]
	if file.Availability != "included" || file.Role != "cover" {
		return "", nil
	}
	reader, ok := repo.(ports.RevisionFileReader)
	if !ok {
		return "", problem.New("REVISION_FILE_UNAVAILABLE", "")
	}
	data, err := reader.ReadRevisionFile(ctx, id, view.Revision, file.BundlePath, maxCoverBytes)
	var code *problem.Error
	if errors.As(err, &code) && code.Code == "REVISION_FILE_LIMIT" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	switch mime := http.DetectContentType(data); mime {
	case "image/png", "image/jpeg", "image/webp":
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
	default:
		return "", nil
	}
}
