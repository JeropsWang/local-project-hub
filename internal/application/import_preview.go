package application

import (
	"context"

	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
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
	return s.reader.InspectArchive(ctx, req.SourcePath)
}
