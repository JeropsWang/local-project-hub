package application

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/adapters/environment"
	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type lifecycleProcesses struct {
	mu       sync.Mutex
	stops    []string
	commands []ports.StopSpec
	fail     bool
	failUnit string
	starts   int
}

type ownerProbe struct{ calls int }

func (p *ownerProbe) Discover(context.Context, ports.ProbeRequest) (ports.Observation, error) {
	p.calls++
	return ports.Observation{}, nil
}

func TestUnverifiedWorkspaceNeverProbesPriorBindings(t *testing.T) {
	ws, _, _, doc := lifecycleFixture(t)
	root := ws.current.Info().Root
	if err := ws.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".local-owner.dpapi"), []byte("synthetic foreign proof"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Open(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	probe := &ownerProbe{}
	checks := NewCheckService(ws, probe, environment.StaticInspector{}, nil)
	_, err := checks.Check(
		context.Background(),
		CheckRequest{
			ProfileRef: project.Ref{
				ProjectID:        project.ID(doc.ProjectID),
				ProfileID:        "development",
				ExpectedRevision: 1,
			},
		},
	)
	if err == nil || err.Error() != "WORKSPACE_REBIND_REQUIRED" || probe.calls != 0 {
		t.Fatalf("used origin bindings before rebind: %d %v", probe.calls, err)
	}
}

func (p *lifecycleProcesses) Start(_ context.Context, spec ports.ProcessSpec) (run.Identity, error) {
	p.starts++
	if spec.UnitID == p.failUnit {
		return run.Identity{}, problem.New("SYNTHETIC_START_FAILED", "")
	}
	return run.Identity{Kind: "process", ControlID: spec.UnitID}, nil
}

func TestFailedRollbackResumeNeverLaunchesAgain(t *testing.T) {
	ws, _, store, doc := lifecycleFixture(t)
	processes := &lifecycleProcesses{fail: true, failUnit: "broken"}
	stop := NewStopExecutor(ws, processes, nil)
	start := NewStartExecutor(ws, processes, nil, stop, nil, nil)
	snapshot := run.Snapshot{
		ID:         "55555555-5555-4555-8555-555555555555",
		ProfileRef: project.Ref{ProjectID: project.ID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 1},
		Document:   doc,
		CreatedAt:  time.Now().UTC(),
		Units: []run.UnitPlan{
			{
				ID:             "api",
				Kind:           "process",
				Control:        "managed",
				ResourceID:     "api",
				InstanceID:     "66666666-6666-4666-8666-666666666666",
				Start:          &run.Command{Executable: "synthetic"},
				StopKind:       "managed-pipe",
				TimeoutSeconds: 1,
			},
			{
				ID:             "broken",
				Kind:           "process",
				Control:        "managed",
				ResourceID:     "broken",
				InstanceID:     "77777777-7777-4777-8777-777777777777",
				Start:          &run.Command{Executable: "synthetic"},
				StopKind:       "managed-pipe",
				TimeoutSeconds: 1,
			},
		},
	}
	plan := task.Plan{Kind: task.Start, WorkspaceID: ws.current.Info().WorkspaceID, RunSnapshot: &snapshot}
	err := start.Execute(context.Background(), plan, lifecycleControl{})
	if err == nil || err.Error() != "PROCESS_STOP_INCOMPLETE" {
		t.Fatal("missing rollback failure", err)
	}
	processes.fail = false
	err = start.Execute(context.Background(), plan, lifecycleControl{})
	if err == nil || err.Error() != "SYNTHETIC_START_FAILED" || processes.starts != 2 {
		t.Fatalf("replayed startup after failed rollback: %d %v", processes.starts, err)
	}
	record, err := store.Run(context.Background(), snapshot.ID)
	if err != nil || record.State != "failed-start" {
		t.Fatal("rollback did not converge", err)
	}
}

func TestReadinessFailureRollsBackBeforeStartingDependants(t *testing.T) {
	ws, _, store, doc := lifecycleFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	processes := &lifecycleProcesses{}
	stop := NewStopExecutor(ws, processes, nil)
	start := NewStartExecutor(ws, processes, nil, stop, nil, nil)
	snapshot := run.Snapshot{
		ID:         "55555555-5555-4555-8555-555555555555",
		ProfileRef: project.Ref{ProjectID: project.ID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 1},
		Document:   doc,
		CreatedAt:  time.Now().UTC(),
		Units: []run.UnitPlan{
			{
				ID:             "api",
				Kind:           "process",
				Control:        "managed",
				ResourceID:     "api",
				InstanceID:     "66666666-6666-4666-8666-666666666666",
				Start:          &run.Command{Executable: "synthetic"},
				StopKind:       "managed-pipe",
				TimeoutSeconds: 1,
				Readiness:      &project.Probe{Kind: "tcp", Host: "127.0.0.1", Port: port, TimeoutSeconds: 1},
			},
			{
				ID:             "dependent",
				Kind:           "process",
				Control:        "managed",
				ResourceID:     "dependent",
				InstanceID:     "77777777-7777-4777-8777-777777777777",
				Start:          &run.Command{Executable: "synthetic"},
				StopKind:       "managed-pipe",
				TimeoutSeconds: 1,
			},
		},
	}
	start.readiness = environment.NewReadinessAdapter(processes, nil, nil)
	err = start.Execute(
		context.Background(),
		task.Plan{Kind: task.Start, WorkspaceID: ws.current.Info().WorkspaceID, RunSnapshot: &snapshot},
		lifecycleControl{},
	)
	if err == nil || err.Error() != "READINESS_TIMEOUT" || processes.starts != 1 ||
		strings.Join(processes.stops, ",") != "api" {
		t.Fatalf("readiness bypass: starts=%d stops=%v error=%v", processes.starts, processes.stops, err)
	}
	record, err := store.Run(context.Background(), snapshot.ID)
	if err != nil || record.State != "failed-start" {
		t.Fatal("readiness failure not rolled back", err)
	}
}
func (p *lifecycleProcesses) Inspect(context.Context, run.Identity) (ports.InstanceState, error) {
	return ports.InstanceState{RootRunning: true, ActiveProcesses: 1}, nil
}

func (p *lifecycleProcesses) Stop(
	_ context.Context,
	identity run.Identity,
	spec ports.StopSpec,
) (ports.StopResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stops = append(p.stops, identity.ControlID)
	p.commands = append(p.commands, spec)
	return ports.StopResult{Graceful: !p.fail, Code: "SYNTHETIC_STOP"}, nil
}

func lifecycleFixture(t *testing.T) (*WorkspaceService, *ProjectService, ports.RunRepository, project.Document) {
	t.Helper()
	ws := NewWorkspaceService(persistence.NewFactory())
	_, err := ws.Initialize(context.Background(), InitWorkspaceRequest{Root: t.TempDir(), Name: "lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	data, err := os.ReadFile("../../docs/contracts/examples/demo-bundle/project.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc project.Document
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	projects := NewProjectService(ws, contract.DecodeProject)
	if _, err = projects.Register(context.Background(), RegisterProjectRequest{Document: doc}); err != nil {
		t.Fatal(err)
	}
	return ws, projects, ws.current.(ports.RunRepository), doc
}

func seedLifecycleRun(t *testing.T, store ports.RunRepository, doc project.Document) run.Record {
	t.Helper()
	snapshot := run.Snapshot{
		ID:         "11111111-1111-4111-8111-111111111111",
		ProfileRef: project.Ref{ProjectID: project.ID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 1},
		Document:   doc,
		CreatedAt:  time.Now().UTC(),
		Units: []run.UnitPlan{
			{
				ID:             "api",
				Kind:           "process",
				Control:        "managed",
				ResourceID:     "api",
				StopKind:       "command",
				Stop:           &run.Command{Executable: "original-stop.exe", Args: []string{"original"}},
				TimeoutSeconds: 1,
			},
		},
	}
	if err := store.CreateRun(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	record, err := store.Run(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.State = "running"
	record.Acquisitions = []run.Acquisition{
		{
			UnitID:        "api",
			ResourceID:    "api",
			Identity:      run.Identity{Kind: "process", ControlID: "api"},
			NewlyAcquired: true,
			Phase:         "active",
		},
	}
	if err = store.UpdateRun(context.Background(), record, record.Version); err != nil {
		t.Fatal(err)
	}
	record, err = store.Run(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestEditedConfigCannotChangeRunningStop(t *testing.T) {
	ws, projects, store, doc := lifecycleFixture(t)
	seedLifecycleRun(t, store, doc)
	listed, err := projects.Get(context.Background(), project.ID(doc.ProjectID))
	if err != nil || listed.RunSummary != "running" {
		t.Fatalf("running project shown stopped: %+v %v", listed, err)
	}
	doc.Name = "edited after launch"
	if _, err := projects.Save(context.Background(), SaveProjectRequest{RegisterProjectRequest: RegisterProjectRequest{Document: doc}, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	plans := NewPlanService(ws, nil)
	plan, err := plans.BuildStop(
		context.Background(),
		ProfileRef{ProjectID: project.ID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	processes := &lifecycleProcesses{}
	service := NewTaskService(
		ws,
		plans,
		map[task.Kind]ports.TaskExecutor{task.Stop: NewStopExecutor(ws, processes, nil)},
	)
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	view, err := service.Submit(
		context.Background(),
		SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: "stop-original"},
	)
	if err != nil {
		t.Fatal(err)
	}
	waitLifecycleTask(t, service, view.ID, task.Succeeded)
	if len(processes.commands) != 1 || processes.commands[0].Command.Executable != "original-stop.exe" ||
		strings.Join(processes.commands[0].Command.Args, ",") != "original" {
		t.Fatalf("mutable stop: %+v", processes.commands)
	}
	finished, err := store.Run(context.Background(), "11111111-1111-4111-8111-111111111111")
	if err != nil || finished.State != "stopped" || finished.Snapshot.ProfileRef.ExpectedRevision != 1 {
		t.Fatalf("lost started revision: %+v %v", finished, err)
	}
}

func waitLifecycleTask(t *testing.T, s *TaskService, id task.ID, state task.State) TaskView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, err := s.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if view.State == state {
			return view
		}
		if view.State == task.Failed {
			t.Fatalf("task failed: %+v", view)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("did not reach %s", state)
	return TaskView{}
}

func TestQuitFailureCanResumeOrAbortWithoutDeadlock(t *testing.T) {
	for _, action := range []string{"continue-wait", "abort-exit"} {
		t.Run(action, func(t *testing.T) {
			ws, _, store, doc := lifecycleFixture(t)
			seedLifecycleRun(t, store, doc)
			processes := &lifecycleProcesses{fail: true}
			plans := NewPlanService(ws, nil)
			service := NewTaskService(ws, plans, map[task.Kind]ports.TaskExecutor{})
			t.Cleanup(func() { _ = service.Close(context.Background()) })
			lifecycle := NewLifecycleService(ws, service, NewStopExecutor(ws, processes, nil))
			ctx := context.Background()
			view, err := lifecycle.RequestExit(ctx)
			if err != nil {
				t.Fatal(err)
			}
			waiting := waitLifecycleTask(t, service, view.ID, task.WaitingUser)
			if waiting.Cancelable {
				t.Fatal("exit exposes unsafe generic cancellation")
			}
			repeated, err := lifecycle.RequestExit(ctx)
			if err != nil || repeated.ID != view.ID {
				t.Fatal("duplicate exit", err)
			}
			if allowed, err := lifecycle.CanQuit(ctx); err != nil || allowed {
				t.Fatal("quit before graceful stop", err)
			}
			if _, err = service.Submit(ctx, SubmitPlanRequest{PlanID: "irrelevant", RequestID: "during-exit"}); err == nil ||
				err.Error() != "EXIT_IN_PROGRESS" {
				t.Fatal("admitted work during exit", err)
			}
			if _, err = service.Resolve(ctx, ResolveTaskRequest{TaskID: view.ID, ExpectedEventSeq: "0", Action: action}); err == nil ||
				err.Error() != "TASK_CONFLICT" {
				t.Fatal("stale resolve accepted", err)
			}
			processes.mu.Lock()
			processes.fail = false
			processes.mu.Unlock()
			resolved, err := service.Resolve(
				ctx,
				ResolveTaskRequest{TaskID: view.ID, ExpectedEventSeq: waiting.LastEventSeq, Action: action},
			)
			if err != nil || resolved.ID != view.ID {
				t.Fatal("changed coordinator", err)
			}
			if action == "abort-exit" {
				waitLifecycleTask(t, service, view.ID, task.Cancelled)
				if _, err = service.Submit(ctx, SubmitPlanRequest{PlanID: "irrelevant", RequestID: "after-abort"}); err == nil ||
					err.Error() == "EXIT_IN_PROGRESS" {
					t.Fatal("admission not restored", err)
				}
				view, err = lifecycle.RequestExit(ctx)
				if err != nil {
					t.Fatal(err)
				}
			}
			waitLifecycleTask(t, service, view.ID, task.Succeeded)
			if allowed, err := lifecycle.CanQuit(ctx); err != nil || !allowed {
				t.Fatal("quit remained blocked", err)
			}
			events, err := service.Events(ctx, view.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			allowCount := 0
			for _, event := range events {
				if event.Payload.Code == "MANAGER_ALLOW_QUIT" {
					allowCount++
				}
			}
			if allowCount != 1 {
				t.Fatal("missing/duplicate allow-quit event", allowCount)
			}
		})
	}
}

type lifecycleControl struct{}

type exitSafePointExecutor struct {
	entered chan struct{}
	release chan struct{}
}

func (e *exitSafePointExecutor) Execute(ctx context.Context, _ task.Plan, control ports.TaskControl) error {
	close(e.entered)
	<-e.release
	return control.Checkpoint(ctx)
}

func TestExitCancelsQueueAndWaitsForSafePreparationPoint(t *testing.T) {
	ws, _, _, doc := lifecycleFixture(t)
	ctx := context.Background()
	plans := NewPlanService(ws, NewCheckService(ws, &ownerProbe{}, environment.StaticInspector{}, nil))
	prepare := &exitSafePointExecutor{entered: make(chan struct{}), release: make(chan struct{})}
	tasks := NewTaskService(ws, plans, map[task.Kind]ports.TaskExecutor{task.Prepare: prepare})
	t.Cleanup(func() { _ = tasks.Close(ctx) })
	lifecycle := NewLifecycleService(ws, tasks, NewStopExecutor(ws, nil, nil))
	plan, err := plans.BuildPrepare(
		ctx,
		project.Ref{ProjectID: project.ID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err := tasks.Submit(
		ctx,
		SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: "safe-first"},
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-prepare.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not enter safe wait")
	}
	second, err := tasks.Submit(
		ctx,
		SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: "safe-second"},
	)
	if err != nil {
		t.Fatal(err)
	}
	exit, err := lifecycle.RequestExit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view, err := tasks.Get(ctx, first.ID); err != nil || view.State != task.CancelRequested {
		t.Fatal("active safe wait was interrupted", err)
	}
	if view, err := tasks.Get(ctx, second.ID); err != nil || view.State != task.Cancelled {
		t.Fatal("queued preparation not cancelled", err)
	}
	if allowed, err := lifecycle.CanQuit(ctx); err != nil || allowed {
		t.Fatal("quit before the preparation safe point", err)
	}
	close(prepare.release)
	waitLifecycleTask(t, tasks, first.ID, task.Cancelled)
	waitLifecycleTask(t, tasks, exit.ID, task.Succeeded)
}

type resumedLifecycleControl struct {
	lifecycleControl
	action string
}

func (c resumedLifecycleControl) ResumeAction() string { return c.action }

func TestContinueWaitStartsNextStopNormally(t *testing.T) {
	ws, _, store, doc := lifecycleFixture(t)
	snapshot := run.Snapshot{
		ID:         "11111111-1111-4111-8111-111111111111",
		ProfileRef: project.Ref{ProjectID: project.ID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 1},
		Document:   doc,
		CreatedAt:  time.Now().UTC(),
		Units: []run.UnitPlan{
			{
				ID:             "database",
				Kind:           "process",
				Control:        "managed",
				ResourceID:     "database",
				StopKind:       "managed-pipe",
				TimeoutSeconds: 1,
			},
			{
				ID:             "api",
				Kind:           "process",
				Control:        "managed",
				ResourceID:     "api",
				StopKind:       "managed-pipe",
				TimeoutSeconds: 1,
			},
		},
	}
	ctx := context.Background()
	if err := store.CreateRun(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	record, err := store.Run(ctx, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.State = "running"
	for _, unit := range snapshot.Units {
		record.Acquisitions = append(
			record.Acquisitions,
			run.Acquisition{
				UnitID:        unit.ID,
				ResourceID:    unit.ResourceID,
				Identity:      run.Identity{Kind: "process", ControlID: unit.ID},
				NewlyAcquired: true,
				Phase:         "active",
			},
		)
	}
	if err = saveRun(ctx, store, &record); err != nil {
		t.Fatal(err)
	}
	processes := &lifecycleProcesses{fail: true}
	stop := NewStopExecutor(ws, processes, nil)
	if err = stop.stopRun(ctx, store, &record, false, false, lifecycleControl{}); err == nil {
		t.Fatal("missing initial stop wait")
	}
	processes.fail = false
	if err = stop.stopRun(ctx, store, &record, false, false, resumedLifecycleControl{action: "continue-wait"}); err != nil {
		t.Fatal(err)
	}
	if len(processes.commands) != 3 || !processes.commands[1].WaitOnly || processes.commands[2].WaitOnly {
		t.Fatalf("continue-wait leaked into a stop not yet started: %+v", processes.commands)
	}
	if processes.commands[0].OperationID != processes.commands[1].OperationID {
		t.Fatal("continue-wait replaced the pending operation")
	}
}

func (lifecycleControl) Checkpoint(context.Context) error               { return nil }
func (lifecycleControl) Progress(context.Context, task.EventData) error { return nil }
func (lifecycleControl) ResumeAction() string                           { return "" }

func TestRollbackOnlyNewAcquisitionsAndStopSharedLast(t *testing.T) {
	ws, _, store, doc := lifecycleFixture(t)
	resources := ws.current.(ports.ResourceRepository)
	ctx := context.Background()
	reference, err := resources.SaveResource(
		ctx,
		resource.Record{
			SchemaVersion: "1.0",
			ResourceID:    "22222222-2222-4222-8222-222222222222",
			Control:       "reference",
			Engine:        "desktop",
			EngineID:      "engine",
			Instances:     []run.Identity{{Kind: "container", ContainerID: "external"}},
		},
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	database, err := resources.SaveResource(
		ctx,
		resource.Record{
			SchemaVersion: "1.0",
			ResourceID:    "33333333-3333-4333-8333-333333333333",
			Control:       "managed",
			Engine:        "desktop",
			EngineID:      "engine",
			Shared:        true,
			Active:        true,
			ControlGrant:  "44444444-4444-4444-8444-444444444444",
			Instances:     []run.Identity{{Kind: "container", ContainerID: "database"}},
		},
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	containers := &lifecycleContainers{}
	resourceService := NewResourceService(ws, containers, nil)
	processes := &lifecycleProcesses{failUnit: "broken"}
	stop := NewStopExecutor(ws, processes, resourceService)
	start := NewStartExecutor(ws, processes, containers, stop, nil, nil)
	snapshot := run.Snapshot{
		ID:         "55555555-5555-4555-8555-555555555555",
		ProfileRef: project.Ref{ProjectID: project.ID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 1},
		Document:   doc,
		CreatedAt:  time.Now().UTC(),
		Units: []run.UnitPlan{
			{
				ID:                  "external",
				Kind:                "external",
				Control:             "reference",
				ResourceID:          reference.ResourceID,
				ResourceFingerprint: reference.Fingerprint,
			},
			{
				ID:                  "database",
				Kind:                "compose",
				Control:             "managed",
				ResourceID:          database.ResourceID,
				ResourceFingerprint: database.Fingerprint,
			},
			{
				ID:             "api",
				Kind:           "process",
				Control:        "managed",
				ResourceID:     "api",
				InstanceID:     "66666666-6666-4666-8666-666666666666",
				Start:          &run.Command{Executable: "synthetic"},
				StopKind:       "managed-pipe",
				TimeoutSeconds: 1,
			},
			{
				ID:             "broken",
				Kind:           "process",
				Control:        "managed",
				ResourceID:     "broken",
				InstanceID:     "77777777-7777-4777-8777-777777777777",
				Start:          &run.Command{Executable: "synthetic"},
				StopKind:       "managed-pipe",
				TimeoutSeconds: 1,
			},
		},
	}
	plan := task.Plan{ID: "test", Kind: task.Start, WorkspaceID: ws.current.Info().WorkspaceID, RunSnapshot: &snapshot}
	if err = start.Execute(ctx, plan, lifecycleControl{}); err == nil {
		t.Fatal("failed start succeeded")
	}
	if strings.Join(processes.stops, ",") != "api" || len(containers.stops) != 0 {
		t.Fatalf("wrong rollback: %v %v", processes.stops, containers.stops)
	}
	rolledBack, err := store.Run(ctx, snapshot.ID)
	if err != nil || rolledBack.State != "failed-start" {
		t.Fatalf("rollback not journaled: %+v %v", rolledBack, err)
	}
	// A normal second run consumes the same shared database. Global Quit must
	// release its API before the database and never stop the external reference.
	snapshot.ID = "88888888-8888-4888-8888-888888888888"
	snapshot.Units = snapshot.Units[:3]
	if err = start.Execute(ctx, task.Plan{Kind: task.Start, WorkspaceID: plan.WorkspaceID, RunSnapshot: &snapshot}, lifecycleControl{}); err != nil {
		t.Fatal(err)
	}
	plans := NewPlanService(ws, nil)
	tasks := NewTaskService(ws, plans, map[task.Kind]ports.TaskExecutor{})
	t.Cleanup(func() { _ = tasks.Close(ctx) })
	exit := NewLifecycleService(ws, tasks, stop)
	view, err := exit.RequestExit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitLifecycleTask(t, tasks, view.ID, task.Succeeded)
	if strings.Join(processes.stops, ",") != "api,api" || strings.Join(containers.stops, ",") != "database" {
		t.Fatalf("wrong shutdown: %v %v", processes.stops, containers.stops)
	}
}

type lifecycleContainers struct{ stops []string }

func (*lifecycleContainers) Inventory(context.Context, string) ([]ports.ContainerObservation, error) {
	return nil, nil
}
func (*lifecycleContainers) Start(context.Context, ports.ComposeSpec) ([]run.Identity, error) {
	return nil, nil
}
func (c *lifecycleContainers) Stop(_ context.Context, id run.Identity) (ports.ContainerStopResult, error) {
	c.stops = append(c.stops, id.ContainerID)
	return ports.ContainerStopResult{Graceful: true}, nil
}
