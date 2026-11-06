package application

import (
	"context"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type ImportPreviewRequest struct {
	SourcePath string `json:"sourcePath"`
}

// PreviewImport reports package evidence without registering a project. A later
// commit must revalidate this content identity before applying target bindings.
func (s *BundleService) PreviewImport(ctx context.Context, req ImportPreviewRequest) (bundle.Inspection, error) {
	if s.reader == nil {
		return bundle.Inspection{}, problem.New("IMPORT_READER_UNAVAILABLE", "")
	}
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo := s.workspaces.current
	if repo == nil {
		return bundle.Inspection{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	if repo.Info().State != "ready" {
		return bundle.Inspection{}, problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	store, ok := repo.(ports.ImportPreviewRepository)
	if !ok {
		return bundle.Inspection{}, problem.New("IMPORT_STORAGE_UNAVAILABLE", "")
	}
	payload, err := s.reader.ReadArchive(ctx, req.SourcePath)
	if err != nil {
		return bundle.Inspection{}, err
	}
	preview := payload.Inspection
	preview.ID, err = identity.NewUUID()
	if err != nil {
		return bundle.Inspection{}, err
	}
	preview.ExpiresAt = time.Now().UTC().Add(30 * time.Minute).Truncate(time.Second)
	err = store.SaveImportPreview(
		ctx,
		preview.ID,
		bundle.ImportPlan{SourcePath: req.SourcePath, Identity: preview.Identity,
			ProjectID: preview.Document.ProjectID, ExpiresAt: preview.ExpiresAt},
	)
	return preview, err
}
