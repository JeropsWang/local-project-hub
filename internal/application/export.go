package application

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type ExportRequest struct {
	ProfileRef
	DestinationPath string
}

// BundleService coordinates portability through durable backend plans.
type BundleService struct {
	workspaces *WorkspaceService
	writer     ports.ArchiveWriter
	reader     ports.ArchiveReader
}

func NewBundleService(ws *WorkspaceService, writer ports.ArchiveWriter, reader ports.ArchiveReader) *BundleService {
	return &BundleService{workspaces: ws, writer: writer, reader: reader}
}

// PlanExport records the fixed revision, byte inventory and chosen destination.
func (s *BundleService) PlanExport(ctx context.Context, req ExportRequest) (task.Plan, error) {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo := s.workspaces.current
	if repo == nil {
		return task.Plan{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	id, err := identity.NewUUID()
	if err != nil {
		return task.Plan{}, err
	}
	plan := task.Plan{ID: task.PlanID(id), Kind: task.Export, WorkspaceID: repo.Info().WorkspaceID,
		ProfileRef: &req.ProfileRef, ExportDestination: req.DestinationPath, CreatedAt: time.Now().UTC(),
		Steps: []task.Step{
			{ID: "configuration-package", Action: "export-configuration", Effect: "create-zip", CancelPolicy: "wait"},
		}}
	input, err := exportPlanInput(ctx, repo, plan)
	if err != nil {
		return task.Plan{}, err
	}
	if s.writer == nil {
		return task.Plan{}, problem.New("EXPORT_EXECUTOR_UNAVAILABLE", "")
	}
	closure, err := s.writer.Inspect(input)
	if err != nil {
		return task.Plan{}, err
	}
	plan.Warnings = closure.Warnings
	plan.RequiredBindings = closure.ExternalBindingKeys
	plan.Fingerprint = exportFingerprint(input)
	store, err := taskStorage(repo)
	if err != nil {
		return task.Plan{}, err
	}
	if err = store.SavePlan(ctx, plan); err != nil {
		return task.Plan{}, err
	}
	return plan, nil
}

func exportPlanInput(ctx context.Context, repo ports.Repository, plan task.Plan) (bundle.ExportInput, error) {
	if repo == nil || repo.Info().WorkspaceID != plan.WorkspaceID || repo.Info().State != "ready" ||
		plan.ProfileRef == nil {
		return bundle.ExportInput{}, problem.New("PLAN_STALE", "")
	}
	if !filepath.IsAbs(plan.ExportDestination) {
		return bundle.ExportInput{}, problem.New("EXPORT_DESTINATION_INVALID", "")
	}
	reader, ok := repo.(ports.RevisionArchiveReader)
	if !ok {
		return bundle.ExportInput{}, problem.New("EXPORT_STORAGE_UNAVAILABLE", "")
	}
	input, err := reader.ExportRevision(ctx, *plan.ProfileRef)
	if err != nil {
		return bundle.ExportInput{}, err
	}
	input.Destination = plan.ExportDestination
	if plan.Fingerprint != "" && exportFingerprint(input) != plan.Fingerprint {
		return bundle.ExportInput{}, problem.New("PLAN_STALE", "")
	}
	return input, nil
}

func exportFingerprint(input bundle.ExportInput) string {
	hashes := map[string]string{bundle.ProjectFile: bundle.Digest(input.Project)}
	for name, data := range input.Files {
		hashes[name] = bundle.Digest(data)
	}
	data, _ := json.Marshal(struct {
		Destination string
		Hashes      map[string]string
	}{input.Destination, hashes})
	return bundle.Digest(data)
}

// Execute publishes only after the stored plan and revision have been checked.
func (s *BundleService) Execute(ctx context.Context, plan task.Plan, control ports.TaskControl) error {
	if plan.Kind != task.Export || s.writer == nil {
		return problem.New("EXPORT_EXECUTOR_UNAVAILABLE", "")
	}
	s.workspaces.mu.RLock()
	input, err := exportPlanInput(ctx, s.workspaces.current, plan)
	s.workspaces.mu.RUnlock()
	if err != nil {
		return err
	}
	if err = control.Checkpoint(ctx); err != nil {
		return err
	}
	// Publication is one wait step so a late cancellation cannot report an
	// already published package as cancelled.
	_, err = s.writer.Write(ctx, input)
	return err
}
