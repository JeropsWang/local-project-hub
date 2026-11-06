package application

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type CommitImportRequest struct {
	PreviewID string            `json:"previewId"`
	Bindings  []project.Binding `json:"bindings"`
}

// PlanImport pins the preview and explicit target bindings before registration.
func (s *BundleService) PlanImport(ctx context.Context, req CommitImportRequest) (task.Plan, error) {
	if err := validateBindings(req.Bindings); err != nil {
		return task.Plan{}, err
	}
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo := s.workspaces.current
	if repo == nil {
		return task.Plan{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	store, ok := repo.(ports.ImportPreviewRepository)
	if !ok {
		return task.Plan{}, problem.New("IMPORT_STORAGE_UNAVAILABLE", "")
	}
	preview, err := store.LoadImportPreview(ctx, req.PreviewID)
	if err != nil {
		return task.Plan{}, err
	}
	preview.Bindings = slices.Clone(req.Bindings)
	id, err := identity.NewUUID()
	if err != nil {
		return task.Plan{}, err
	}
	plan := task.Plan{
		ID:             task.PlanID(id),
		Kind:           task.Import,
		WorkspaceID:    repo.Info().WorkspaceID,
		ImportSnapshot: &preview,
		CreatedAt: time.Now().
			UTC(),
		Steps: []task.Step{
			{ID: "register-project", Action: "register-project", Effect: "register", CancelPolicy: "wait"},
		},
	}
	plan.Fingerprint = importFingerprint(plan)
	payload, err := s.importPayload(ctx, repo, plan)
	if err != nil {
		return task.Plan{}, err
	}
	plan.Warnings = payload.Inspection.Warnings
	for _, key := range payload.Inspection.RequiredBindings {
		if !slices.ContainsFunc(req.Bindings, func(binding project.Binding) bool { return binding.Key == key }) {
			plan.RequiredBindings = append(plan.RequiredBindings, key)
		}
	}
	tasks, err := taskStorage(repo)
	if err != nil {
		return task.Plan{}, err
	}
	if err = tasks.SavePlan(ctx, plan); err != nil {
		return task.Plan{}, err
	}
	return plan, nil
}

func importFingerprint(plan task.Plan) string {
	data, _ := json.Marshal(struct {
		Workspace string
		Snapshot  *bundle.ImportPlan
	}{plan.WorkspaceID, plan.ImportSnapshot})
	return bundle.Digest(data)
}

func validateImportTarget(ctx context.Context, repo ports.Repository, plan task.Plan) error {
	preview := plan.ImportSnapshot
	if repo == nil || repo.Info().State != "ready" || repo.Info().WorkspaceID != plan.WorkspaceID ||
		preview == nil || importFingerprint(plan) != plan.Fingerprint {
		return problem.New("PLAN_STALE", "")
	}
	if !time.Now().Before(preview.ExpiresAt) {
		return problem.New("IMPORT_PREVIEW_EXPIRED", "")
	}
	if _, err := repo.Get(ctx, preview.ProjectID); err == nil {
		return problem.New("PROJECT_EXISTS", preview.ProjectID)
	} else if err.Error() != "PROJECT_MISSING" {
		return err
	}
	return validateBindings(preview.Bindings)
}

func (s *BundleService) importPayload(
	ctx context.Context,
	repo ports.Repository,
	plan task.Plan,
) (bundle.ImportPayload, error) {
	if err := validateImportTarget(ctx, repo, plan); err != nil {
		return bundle.ImportPayload{}, err
	}
	if s.reader == nil {
		return bundle.ImportPayload{}, problem.New("IMPORT_READER_UNAVAILABLE", "")
	}
	preview := plan.ImportSnapshot
	payload, err := s.reader.ReadArchive(ctx, preview.SourcePath)
	if err != nil {
		return bundle.ImportPayload{}, err
	}
	if payload.Inspection.Identity != preview.Identity || payload.Inspection.Document.ProjectID != preview.ProjectID {
		return bundle.ImportPayload{}, problem.New("BUNDLE_CHANGED", "")
	}
	for _, binding := range preview.Bindings {
		if !slices.Contains(payload.Inspection.RequiredBindings, binding.Key) {
			return bundle.ImportPayload{}, problem.New("BINDING_INVALID", binding.Key)
		}
	}
	// An imported UUID alone cannot join an existing target control grant.
	if resources, ok := repo.(ports.ResourceRepository); ok {
		for _, unit := range payload.Inspection.Document.Units {
			if unit.Kind != "compose" && unit.Kind != "external" {
				continue
			}
			if _, err := resources.Resource(ctx, unit.ResourceID); err == nil {
				return bundle.ImportPayload{}, problem.New("IMPORT_RESOURCE_REBIND_REQUIRED", unit.ResourceID)
			} else if err.Error() != "RESOURCE_MISSING" {
				return bundle.ImportPayload{}, err
			}
		}
	}
	return payload, nil
}

func (s *BundleService) executeImport(ctx context.Context, plan task.Plan, control ports.TaskControl) error {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo := s.workspaces.current
	payload, err := s.importPayload(ctx, repo, plan)
	if err != nil {
		return err
	}
	if err = control.Checkpoint(ctx); err != nil {
		return err
	}
	// The revision transaction owns publication and cleanup. Register receives
	// the exact checked bytes, so replacing the ZIP here cannot change the commit.
	_, err = repo.Register(
		ctx,
		payload.Inspection.Document,
		payload.Project,
		plan.ImportSnapshot.Bindings,
		payload.Files,
	)
	return err
}
