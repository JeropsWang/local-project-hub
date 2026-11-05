package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// StartExecutor performs a preflight for the entire profile before acquiring its
// first unit. External effects are journaled; failures roll back only new work.
type StartExecutor struct {
	workspaces *WorkspaceService
	processes  ports.ProcessAdapter
	containers ports.ContainerAdapter
	stop       *StopExecutor
	secrets    ports.SecretResolver
	units      ports.UnitRunner
	readiness  ports.ReadinessAdapter
}

// StartOption adds checked backend capabilities; none is read from project JSON.
type StartOption func(*StartExecutor)

func StartWithReadiness(adapter ports.ReadinessAdapter) StartOption {
	return func(executor *StartExecutor) { executor.readiness = adapter }
}

func NewStartExecutor(
	ws *WorkspaceService,
	processes ports.ProcessAdapter,
	containers ports.ContainerAdapter,
	stop *StopExecutor,
	secrets ports.SecretResolver,
	units ports.UnitRunner,
	options ...StartOption,
) *StartExecutor {
	executor := &StartExecutor{
		workspaces: ws,
		processes:  processes,
		containers: containers,
		stop:       stop,
		secrets:    secrets,
		units:      units,
	}
	for _, option := range options {
		option(executor)
	}
	return executor
}

func (e *StartExecutor) Execute(ctx context.Context, plan task.Plan, control ports.TaskControl) error {
	if plan.RunSnapshot == nil {
		return problem.New("RUN_SNAPSHOT_REQUIRED", "")
	}
	e.workspaces.mu.RLock()
	repo := e.workspaces.current
	if repo == nil || repo.Info().WorkspaceID != plan.WorkspaceID || repo.Info().State != "ready" {
		e.workspaces.mu.RUnlock()
		return problem.New("PLAN_STALE", "")
	}
	store, ok := repo.(ports.RunRepository)
	resources, resourceOK := repo.(ports.ResourceRepository)
	e.workspaces.mu.RUnlock()
	if !ok {
		return problem.New("RUN_STORAGE_UNAVAILABLE", "")
	}
	snapshot := *plan.RunSnapshot
	if e.stop == nil {
		return problem.New("STOP_EXECUTOR_REQUIRED", "")
	}
	if existing, err := store.Run(ctx, snapshot.ID); err == nil {
		if existing.FailureCode == "" {
			return problem.New("RUN_ALREADY_ACTIVE", "")
		}
		if cleanup := e.stop.stopRun(context.WithoutCancel(ctx), store, &existing, true, false, uncancelledControl{TaskControl: control}); cleanup != nil {
			return cleanup
		}
		if existing.FailureCode == "TASK_CANCELLED" {
			return context.Canceled
		}
		return problem.New(existing.FailureCode, "")
	} else if err.Error() != "RUN_MISSING" {
		return err
	}
	commands := map[string]ports.ProcessSpec{}
	for _, unit := range snapshot.Units {
		if unit.Readiness != nil {
			if e.readiness == nil {
				return problem.New("READINESS_ADAPTER_UNAVAILABLE", unit.ID)
			}
			if err := e.readiness.ValidateReadiness(unit); err != nil {
				return err
			}
		}
		switch unit.Kind {
		case "process", "task":
			if unit.Kind == "process" && e.processes == nil {
				return problem.New("PROCESS_ADAPTER_UNAVAILABLE", unit.ID)
			}
			if unit.Control != "managed" || unit.Start == nil ||
				(unit.Kind == "process" && unit.StopKind != "managed-pipe" && (unit.StopKind != "command" || unit.Stop == nil)) {
				return problem.New("AUTOMATIC_STOP_UNSUPPORTED", unit.ID)
			}
			if unit.Kind == "task" && e.units == nil {
				return problem.New("UNIT_RUNNER_UNAVAILABLE", unit.ID)
			}
			spec, err := e.processSpec(ctx, snapshot, unit)
			if err != nil {
				return err
			}
			commands[unit.ID] = spec
		case "compose", "external":
			if !resourceOK {
				return problem.New("RESOURCE_STORAGE_UNAVAILABLE", unit.ID)
			}
			record, err := resources.Resource(ctx, unit.ResourceID)
			if err != nil {
				return err
			}
			if record.Fingerprint == "" || record.Fingerprint != unit.ResourceFingerprint || record.PendingStart != "" {
				return problem.New("RESOURCE_BINDING_CONFLICT", unit.ID)
			}
			if unit.Control == "managed" && (record.Control != "managed" || record.ControlGrant == "") {
				return problem.New("RESOURCE_CONTROL_REQUIRED", unit.ID)
			}
			if unit.Kind == "compose" && unit.Control == "managed" && !record.Active && e.containers == nil {
				return problem.New("CONTAINER_ADAPTER_UNAVAILABLE", unit.ID)
			}
		default:
			return problem.New("UNIT_KIND_UNSUPPORTED", unit.ID)
		}
	}
	if err := control.Checkpoint(ctx); err != nil {
		return err
	}
	if err := store.CreateRun(ctx, snapshot); err != nil {
		return err
	}
	record, err := store.Run(ctx, snapshot.ID)
	if err != nil {
		return err
	}
	for _, unit := range snapshot.Units {
		if err = control.Checkpoint(ctx); err != nil {
			break
		}
		record.Acquisitions = append(
			record.Acquisitions,
			run.Acquisition{UnitID: unit.ID, ResourceID: unit.ResourceID, Phase: "acquiring"},
		)
		if err = saveRun(ctx, store, &record); err != nil {
			break
		}
		acquisition := &record.Acquisitions[len(record.Acquisitions)-1]
		err = e.acquire(ctx, resources, snapshot, unit, commands[unit.ID], acquisition, control)
		// Even a failed acquisition can return partial identities. Persist those
		// before rollback; a crash between intent and this commit stays uncertain.
		if saveErr := saveRun(ctx, store, &record); saveErr != nil {
			return saveErr
		}
		if err != nil {
			break
		}
		if unit.Readiness != nil {
			err = withPreparationCancellation(
				ctx,
				control,
				func(ctx context.Context) error { return e.readiness.WaitReady(ctx, unit, acquisition.Identity) },
			)
			if err != nil {
				break
			}
		}
		if err = control.Progress(ctx, task.EventData{Code: "RUN_UNIT_ACQUIRED", StepID: unit.ID}); err != nil {
			break
		}
	}
	if err != nil {
		failure := err
		record.FailureCode = safeTaskError(err).Code
		if err == context.Canceled {
			record.FailureCode = "TASK_CANCELLED"
		}
		// Cancellation must not cancel a normal save/stop during rollback.
		rollbackControl := uncancelledControl{TaskControl: control}
		if cleanup := e.stop.stopRun(context.WithoutCancel(ctx), store, &record, true, false, rollbackControl); cleanup != nil {
			return cleanup
		}
		return failure
	}
	record.State = "running"
	return saveRun(ctx, store, &record)
}

type uncancelledControl struct{ ports.TaskControl }

func (uncancelledControl) Checkpoint(context.Context) error { return nil }

func (e *StartExecutor) acquire(
	ctx context.Context,
	resources ports.ResourceRepository,
	snapshot run.Snapshot,
	unit run.UnitPlan,
	spec ports.ProcessSpec,
	acquisition *run.Acquisition,
	control ports.TaskControl,
) error {
	switch unit.Kind {
	case "process":
		if e.processes == nil {
			acquisition.Phase = "released"
			return problem.New("PROCESS_ADAPTER_UNAVAILABLE", unit.ID)
		}
		instance, err := e.processes.Start(ctx, spec)
		if err != nil && instance.ControlID == "" {
			acquisition.Phase = "released"
			return err
		}
		acquisition.Identity = instance
		acquisition.NewlyAcquired = true
		acquisition.Phase = "active"
		return err
	case "task":
		var err error
		if snapshot.Document.Units[unit.ID].CancelPolicy == "checkpoint" {
			err = withPreparationCancellation(
				ctx,
				control,
				func(ctx context.Context) error {
					return e.units.RunUnit(ctx, ports.UnitExecution{Process: spec, CancelPolicy: "checkpoint"})
				},
			)
		} else {
			err = e.units.RunUnit(context.WithoutCancel(ctx), ports.UnitExecution{Process: spec, CancelPolicy: "wait"})
		}
		acquisition.Phase = "released"
		if err != nil && err.Error() == "UNIT_STATE_UNCERTAIN" {
			acquisition.Phase = "acquiring"
		}
		return err
	case "compose", "external":
		record, err := resources.Resource(ctx, unit.ResourceID)
		if err != nil {
			acquisition.Phase = "released"
			return err
		}
		use := resource.Use{ProjectID: string(snapshot.ProfileRef.ProjectID), RunID: snapshot.ID, UnitID: unit.ID}
		if err = resources.AcquireResource(ctx, unit.ResourceID, unit.ResourceFingerprint, use); err != nil {
			acquisition.Phase = "released"
			return err
		}
		acquisition.Phase = "active"
		if unit.Kind == "external" || unit.Control != "managed" || record.Active {
			return nil
		}
		if e.containers == nil {
			return problem.New("CONTAINER_ADAPTER_UNAVAILABLE", unit.ID)
		}
		acquisition.NewlyAcquired = true
		spec := ports.ComposeSpec{
			ResourceID:        record.ResourceID,
			Engine:            record.Engine,
			ExpectedEngineID:  record.EngineID,
			ProjectName:       record.ComposeProject,
			ControlGrant:      record.ControlGrant,
			WorkingDir:        record.WorkingDir,
			Services:          record.Services,
			ConfigFingerprint: record.ConfigFingerprint,
		}
		for _, file := range record.Files {
			spec.Files = append(spec.Files, ports.ComposeFile{Path: file.Path, SHA256: file.SHA256})
		}
		_, err = e.containers.Start(ctx, spec)
		return err
	}
	return problem.New("UNIT_KIND_UNSUPPORTED", unit.ID)
}

func (e *StartExecutor) processSpec(
	ctx context.Context,
	snapshot run.Snapshot,
	unit run.UnitPlan,
) (ports.ProcessSpec, error) {
	start, secrets, err := e.resolveCommand(ctx, *unit.Start)
	if err != nil {
		return ports.ProcessSpec{}, err
	}
	spec := ports.ProcessSpec{
		RunID:        unit.InstanceID,
		UnitID:       unit.ID,
		Command:      start,
		Stop:         ports.StopSpec{Kind: unit.StopKind, Timeout: time.Duration(unit.TimeoutSeconds) * time.Second},
		Scripts:      map[string]ports.ProcessScript{},
		SecretValues: secrets,
	}
	if unit.Stop != nil {
		stop, values, err := e.resolveCommand(ctx, *unit.Stop)
		if err != nil {
			return spec, err
		}
		spec.Stop.Command = &stop
		spec.SecretValues = append(spec.SecretValues, values...)
	}
	for key, data := range snapshot.ScriptBytes {
		sum := sha256.Sum256(data)
		spec.Scripts[key] = ports.ProcessScript{
			Name:   snapshot.FileNames[key],
			Bytes:  data,
			SHA256: hex.EncodeToString(sum[:]),
		}
	}
	return spec, nil
}

func (e *StartExecutor) resolveCommand(
	ctx context.Context,
	command run.Command,
) (ports.ProcessCommand, []string, error) {
	resolved := ports.ProcessCommand{
		Executable:  command.Executable,
		Args:        command.Args,
		CWD:         command.CWD,
		Environment: map[string]string{},
	}
	for key, value := range command.Environment {
		resolved.Environment[key] = value
	}
	values := []string{}
	for key, ref := range command.SecretRefs {
		if e.secrets == nil {
			return resolved, nil, problem.New("SECRET_RESOLVER_UNAVAILABLE", key)
		}
		value, err := e.secrets.ResolveSecret(ctx, ref)
		if err != nil {
			return resolved, nil, err
		}
		if value == "" {
			return resolved, nil, problem.New("SECRET_MISSING", key)
		}
		resolved.Environment[key] = value
		values = append(values, value)
	}
	return resolved, values, nil
}
