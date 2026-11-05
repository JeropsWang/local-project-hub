package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func (s *PlanService) buildRunningStop(ctx context.Context, ref ProfileRef) (OperationPlan, error) {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo := s.workspaces.current
	if repo == nil {
		return OperationPlan{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	store, ok := repo.(ports.RunRepository)
	if !ok {
		return OperationPlan{}, problem.New("RUN_STORAGE_UNAVAILABLE", "")
	}
	active, err := store.ActiveRuns(ctx)
	if err != nil {
		return OperationPlan{}, err
	}
	plan := task.Plan{
		Kind:        task.Stop,
		WorkspaceID: repo.Info().WorkspaceID,
		ProfileRef:  &ref,
		CreatedAt:   time.Now().UTC(),
	}
	snapshots := []run.Snapshot{}
	for _, record := range active {
		if record.Snapshot.ProfileRef.ProjectID != ref.ProjectID ||
			record.Snapshot.ProfileRef.ProfileID != ref.ProfileID {
			continue
		}
		plan.RunIDs = append(plan.RunIDs, record.Snapshot.ID)
		snapshots = append(snapshots, record.Snapshot)
		for _, unit := range record.Snapshot.Units {
			plan.ResourceIDs = append(plan.ResourceIDs, unit.ResourceID)
		}
	}
	if len(plan.RunIDs) == 0 {
		return OperationPlan{}, problem.New("RUN_NOT_ACTIVE", "")
	}
	id, err := identity.NewUUID()
	if err != nil {
		return OperationPlan{}, err
	}
	plan.ID = task.PlanID(id)
	plan.Fingerprint = snapshotFingerprint(snapshots)
	storage, err := taskStorage(repo)
	if err != nil {
		return OperationPlan{}, err
	}
	if err = storage.SavePlan(ctx, plan); err != nil {
		return OperationPlan{}, err
	}
	return plan, nil
}

func snapshotFingerprint(snapshots []run.Snapshot) string {
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].ID < snapshots[j].ID })
	data, _ := json.Marshal(snapshots)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *PlanService) validateLifecyclePlan(ctx context.Context, plan task.Plan) error {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo := s.workspaces.current
	if repo == nil || repo.Info().WorkspaceID != plan.WorkspaceID || repo.Info().State != "ready" {
		return problem.New("PLAN_STALE", "")
	}
	if plan.Kind == task.Exit {
		if plan.Fingerprint != "exit:"+plan.WorkspaceID {
			return problem.New("PLAN_STALE", "")
		}
		return nil
	}
	store, ok := repo.(ports.RunRepository)
	if !ok {
		return problem.New("RUN_STORAGE_UNAVAILABLE", "")
	}
	snapshots := []run.Snapshot{}
	for _, id := range plan.RunIDs {
		record, err := store.Run(ctx, id)
		if err != nil {
			return err
		}
		snapshots = append(snapshots, record.Snapshot)
	}
	if len(snapshots) == 0 || snapshotFingerprint(snapshots) != plan.Fingerprint {
		return problem.New("PLAN_STALE", "")
	}
	return nil
}

// StopExecutor releases units in reverse startup order using only frozen launch
// data. An incomplete unit blocks its dependencies and keeps the run visible.
type StopExecutor struct {
	workspaces *WorkspaceService
	processes  ports.ProcessAdapter
	resources  *ResourceService
}

func NewStopExecutor(ws *WorkspaceService, processes ports.ProcessAdapter, resources *ResourceService) *StopExecutor {
	return &StopExecutor{workspaces: ws, processes: processes, resources: resources}
}

func (e *StopExecutor) repository() (ports.RunRepository, error) {
	e.workspaces.mu.RLock()
	defer e.workspaces.mu.RUnlock()
	if e.workspaces.current == nil {
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	store, ok := e.workspaces.current.(ports.RunRepository)
	if !ok {
		return nil, problem.New("RUN_STORAGE_UNAVAILABLE", "")
	}
	return store, nil
}

func (e *StopExecutor) Execute(ctx context.Context, plan task.Plan, control ports.TaskControl) error {
	store, err := e.repository()
	if err != nil {
		return err
	}
	for _, id := range plan.RunIDs {
		record, err := store.Run(ctx, id)
		if err != nil {
			return err
		}
		if err = e.stopRun(ctx, store, &record, false, false, control); err != nil {
			return err
		}
	}
	return nil
}

func saveRun(ctx context.Context, store ports.RunRepository, record *run.Record) error {
	if err := store.UpdateRun(ctx, *record, record.Version); err != nil {
		return err
	}
	record.Version++
	return nil
}

func (e *StopExecutor) stopRun(
	ctx context.Context,
	store ports.RunRepository,
	record *run.Record,
	rollback, global bool,
	control ports.TaskControl,
) error {
	if record.State == "stopped" || record.State == "failed-start" {
		return nil
	}
	record.State = "stopping"
	if err := saveRun(ctx, store, record); err != nil {
		return err
	}
	for i := len(record.Acquisitions) - 1; i >= 0; i-- {
		acquisition := &record.Acquisitions[i]
		if acquisition.Phase == "released" {
			continue
		}
		if err := control.Checkpoint(ctx); err != nil {
			return err
		}
		var unit *run.UnitPlan
		for j := range record.Snapshot.Units {
			if record.Snapshot.Units[j].ID == acquisition.UnitID {
				unit = &record.Snapshot.Units[j]
				break
			}
		}
		if unit == nil {
			return e.stopWaiting(ctx, store, record, "RUN_UNIT_MISSING")
		}
		if acquisition.Phase == "acquiring" {
			return e.stopWaiting(ctx, store, record, "RUN_ACQUISITION_UNCERTAIN")
		}
		alreadyInitiated := acquisition.Phase == "stopping" && acquisition.OperationID != ""
		if acquisition.OperationID == "" || control.ResumeAction() == "retry" {
			operation, err := identity.NewUUID()
			if err != nil {
				return err
			}
			acquisition.OperationID = operation
		}
		acquisition.Phase = "stopping"
		if err := saveRun(ctx, store, record); err != nil {
			return err
		}
		if unit.Kind == "process" && unit.Control == "managed" && (!rollback || acquisition.NewlyAcquired) {
			if e.processes == nil {
				return e.stopWaiting(ctx, store, record, "PROCESS_ADAPTER_UNAVAILABLE")
			}
			spec := ports.StopSpec{
				Kind:        unit.StopKind,
				OperationID: acquisition.OperationID,
				Timeout:     time.Duration(unit.TimeoutSeconds) * time.Second,
				// Continue only an already-started operation. Once it converges,
				// the next dependency still needs its initial normal stop request.
				WaitOnly: alreadyInitiated && control.ResumeAction() == "continue-wait",
			}
			if unit.Stop != nil {
				spec.Command = &ports.ProcessCommand{
					Executable: unit.Stop.Executable,
					Args:       unit.Stop.Args,
					CWD:        unit.Stop.CWD,
				}
			}
			result, err := e.processes.Stop(ctx, acquisition.Identity, spec)
			if err != nil {
				return e.stopWaiting(ctx, store, record, safeTaskError(err).Code)
			}
			if !result.Graceful {
				return e.stopWaiting(ctx, store, record, "PROCESS_STOP_INCOMPLETE")
			}
		}
		if unit.Kind == "compose" || unit.Kind == "external" {
			if e.resources == nil {
				return e.stopWaiting(ctx, store, record, "RESOURCE_ADAPTER_UNAVAILABLE")
			}
			use := resource.Use{
				ProjectID: string(record.Snapshot.ProfileRef.ProjectID),
				RunID:     record.Snapshot.ID,
				UnitID:    unit.ID,
			}
			if err := e.resources.Release(ctx, unit.ResourceID, use); err != nil {
				return e.stopWaiting(ctx, store, record, safeTaskError(err).Code)
			}
			if unit.Control == "managed" && (!rollback || acquisition.NewlyAcquired) {
				result, err := e.resources.StopUnused(ctx, unit.ResourceID, global)
				if err != nil {
					return e.stopWaiting(ctx, store, record, safeTaskError(err).Code)
				}
				if !result.Stopped && result.Reason != "in-use" && result.Reason != "shared-kept-running" {
					return e.stopWaiting(ctx, store, record, "RESOURCE_STOP_INCOMPLETE")
				}
			}
		}
		acquisition.Phase = "released"
		if err := saveRun(ctx, store, record); err != nil {
			return err
		}
		if err := control.Progress(ctx, task.EventData{Code: "RUN_UNIT_RELEASED", StepID: unit.ID}); err != nil {
			return err
		}
	}
	record.State = "stopped"
	if rollback {
		record.State = "failed-start"
	}
	return saveRun(ctx, store, record)
}

func (e *StopExecutor) stopWaiting(
	ctx context.Context,
	store ports.RunRepository,
	record *run.Record,
	code string,
) error {
	record.State = "stop-failed"
	if err := saveRun(ctx, store, record); err != nil {
		return err
	}
	return &task.WaitingError{
		Code:    code,
		Message: "Normal stop is incomplete. Inspect the run, then retry or continue waiting.",
	}
}
