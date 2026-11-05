package application

import (
	"context"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/preparation"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// PrepareExecutor applies only the pinned supplies from a saved preparation plan.
type PrepareExecutor struct {
	workspaces *WorkspaceService
	provider   ports.RuntimeProvider
	projects   ports.ProjectPreparer
}

// PrepareOption supplies backend capabilities matching those used to build the plan.
type PrepareOption func(*PrepareExecutor)

// PrepareProjectWith enables complete source/dependency preparation after SDK installation.
func PrepareProjectWith(preparer ports.ProjectPreparer) PrepareOption {
	return func(e *PrepareExecutor) { e.projects = preparer }
}

// NewPrepareExecutor receives the workspace-scoped SDK adapter through its port.
func NewPrepareExecutor(
	ws *WorkspaceService,
	provider ports.RuntimeProvider,
	options ...PrepareOption,
) *PrepareExecutor {
	e := &PrepareExecutor{workspaces: ws, provider: provider}
	for _, option := range options {
		option(e)
	}
	return e
}

// Execute uses explicit checkpoints and progress rather than inheriting a frontend call's lifetime.
func (e *PrepareExecutor) Execute(ctx context.Context, plan task.Plan, control ports.TaskControl) error {
	if plan.ProfileRef == nil {
		return problem.New("PLAN_REFERENCE_REQUIRED", "")
	}
	e.workspaces.mu.RLock()
	repo := e.workspaces.current
	if repo == nil || repo.Info().WorkspaceID != plan.WorkspaceID {
		e.workspaces.mu.RUnlock()
		return problem.New("PLAN_STALE", "")
	}
	view, err := repo.Get(ctx, string(plan.ProfileRef.ProjectID))
	e.workspaces.mu.RUnlock()
	if err != nil {
		return err
	}
	if view.Revision != plan.ProfileRef.ExpectedRevision {
		return problem.New("PLAN_STALE", "")
	}
	locations := map[string]ports.RuntimeLocation{}
	for _, step := range plan.Steps {
		if err := control.Checkpoint(ctx); err != nil {
			return err
		}
		if step.Action == "prepare-project" {
			if e.projects == nil || plan.ProjectPreparation == nil ||
				step.InputFingerprints["source"] != plan.ProjectPreparation.Fingerprint {
				return problem.New("SOURCE_PLAN_STALE", "")
			}
			if err := control.Progress(ctx, task.EventData{Code: "PROJECT_PREPARATION_STARTED", StepID: step.ID}); err != nil {
				return err
			}
			var receipt preparation.Receipt
			err := withPreparationCancellation(ctx, control, func(ctx context.Context) error {
				var err error
				receipt, err = e.projects.Prepare(ctx, *plan.ProjectPreparation, locations)
				return err
			})
			if err != nil {
				return err
			}
			// Do not interrupt an atomic selection commit. Cancellation before this
			// point leaves the previous selection and a complete reusable copy.
			if err := control.Checkpoint(ctx); err != nil {
				return err
			}
			e.workspaces.mu.RLock()
			store, ok := repo.(ports.PreparedEnvironmentRepository)
			if e.workspaces.current != repo || !ok {
				e.workspaces.mu.RUnlock()
				return problem.New("ENVIRONMENT_STORAGE_UNAVAILABLE", "")
			}
			err = store.ActivateEnvironment(ctx, receipt)
			e.workspaces.mu.RUnlock()
			if err != nil {
				return err
			}
			if err := control.Progress(ctx, task.EventData{Code: "PROJECT_PREPARATION_COMPLETED", StepID: step.ID}); err != nil {
				return err
			}
			continue
		}
		if step.Action != "prepare-runtime" {
			return problem.New("PREPARATION_ACTION_UNSUPPORTED", step.ID)
		}
		requirement, ok := view.Document.Requirements[step.RequirementID]
		if !ok {
			return problem.New("REQUIREMENT_MISSING", step.RequirementID)
		}
		runtimePlan, err := e.provider.Plan(
			ctx,
			ports.RuntimeRequest{Requirement: requirement, Constraints: plan.SourceConstraints},
		)
		if err != nil {
			return err
		}
		if step.InputFingerprints["runtime"] == "" || runtimePlan.Fingerprint != step.InputFingerprints["runtime"] {
			return problem.New("RUNTIME_PLAN_STALE", step.ID)
		}
		if err := control.Progress(ctx, task.EventData{Code: "RUNTIME_PREPARATION_STARTED", StepID: step.ID}); err != nil {
			return err
		}
		var location ports.RuntimeLocation
		err = withPreparationCancellation(ctx, control, func(ctx context.Context) error {
			var err error
			location, err = e.provider.Install(ctx, runtimePlan)
			return err
		})
		if err != nil {
			return err
		}
		locations[step.RequirementID] = location
		if plan.ProjectPreparation != nil {
			if err := control.Progress(ctx, task.EventData{Code: "RUNTIME_PREPARATION_COMPLETED", StepID: step.ID}); err != nil {
				return err
			}
			continue
		}
		e.workspaces.mu.RLock()
		if e.workspaces.current != repo {
			e.workspaces.mu.RUnlock()
			return problem.New("PLAN_STALE", "")
		}
		store, ok := repo.(ports.EnvironmentRepository)
		if !ok {
			e.workspaces.mu.RUnlock()
			return problem.New("ENVIRONMENT_STORAGE_UNAVAILABLE", "")
		}
		err = store.BindRuntime(ctx, string(plan.ProfileRef.ProjectID), step.RequirementID, location)
		e.workspaces.mu.RUnlock()
		if err != nil {
			return err
		}
		if err := control.Progress(ctx, task.EventData{Code: "RUNTIME_PREPARATION_COMPLETED", StepID: step.ID}); err != nil {
			return err
		}
	}
	return nil
}

// Only disposable preparation work receives cancellation during an operation.
// Existing project shutdown/save operations use their own wait-policy coordination.
func withPreparationCancellation(
	ctx context.Context,
	control ports.TaskControl,
	operation func(context.Context) error,
) error {
	child, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-child.Done():
				return
			case <-ticker.C:
				if err := control.Checkpoint(ctx); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	err := operation(child)
	close(done)
	<-stopped
	if cause := context.Cause(child); cause != nil {
		return cause
	}
	return err
}
