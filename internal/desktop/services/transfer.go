package services

import (
	"context"

	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
)

func (m *Manager) PreviewImport(ctx context.Context, path string) (bundle.Inspection, error) {
	session, err := m.current()
	if err != nil {
		return bundle.Inspection{}, err
	}
	return session.Bundles.PreviewImport(ctx, application.ImportPreviewRequest{SourcePath: path})
}

func (m *Manager) PlanImport(ctx context.Context, req application.CommitImportRequest) (PlanPreview, error) {
	session, err := m.current()
	if err != nil {
		return PlanPreview{}, err
	}
	plan, err := session.Bundles.PlanImport(ctx, req)
	return preview(plan), err
}

func (m *Manager) PlanExport(ctx context.Context, ref application.ProfileRef, path string) (PlanPreview, error) {
	session, err := m.current()
	if err != nil {
		return PlanPreview{}, err
	}
	plan, err := session.Bundles.PlanExport(ctx, application.ExportRequest{ProfileRef: ref, DestinationPath: path})
	return preview(plan), err
}
