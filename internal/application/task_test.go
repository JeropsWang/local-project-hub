package application_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type countExecutor struct{ calls atomic.Int32 }

type panicExecutor struct{}

func (panicExecutor) Execute(context.Context, task.Plan, ports.TaskControl) error {
	panic("synthetic executor failure")
}

func TestExecutorPanicBecomesFailedTask(t *testing.T) {
	ws, projects := services(t)
	ctx := context.Background()
	doc := declaration(t, "11111111-1111-4111-8111-111111111111")
	if _, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc}); err != nil {
		t.Fatal(err)
	}
	plans := application.NewPlanService(ws, application.NewCheckService(ws, &boundedProbe{}, noFiles{}, nil))
	service := application.NewTaskService(ws, plans, map[task.Kind]ports.TaskExecutor{task.Prepare: panicExecutor{}})
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	plan, err := plans.BuildPrepare(ctx, application.ProfileRef{ProjectID: application.ProjectID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.Submit(ctx, application.SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: "panic-test"})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitTask(t, service, view.ID, task.Failed)
	if failed.Error == nil || failed.Error.Code != "TASK_EXECUTOR_PANICKED" {
		t.Fatalf("panic not reported safely: %+v", failed)
	}
}

func (e *countExecutor) Execute(ctx context.Context, plan task.Plan, control ports.TaskControl) error {
	if err := control.Checkpoint(ctx); err != nil {
		return err
	}
	e.calls.Add(1)
	return control.Progress(ctx, task.EventData{Code: "SYNTHETIC_EFFECT_COMPLETED"})
}

func waitTask(t *testing.T, service *application.TaskService, id application.TaskID, want task.State) application.TaskView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, err := service.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if view.State == want {
			return view
		}
		if view.State == task.Failed {
			t.Fatalf("unexpected failed task: %+v", view)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task did not reach %s", want)
	return application.TaskView{}
}

func TestSubmitIdempotentAndRejectsStalePlan(t *testing.T) {
	ws, projects := services(t)
	ctx := context.Background()
	doc := declaration(t, "11111111-1111-4111-8111-111111111111")
	view, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc})
	if err != nil {
		t.Fatal(err)
	}
	checks := application.NewCheckService(ws, &boundedProbe{}, noFiles{}, nil)
	plans := application.NewPlanService(ws, checks)
	executor := &countExecutor{}
	service := application.NewTaskService(ws, plans, map[task.Kind]ports.TaskExecutor{task.Prepare: executor})
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	ref := application.ProfileRef{ProjectID: application.ProjectID(doc.ProjectID), ProfileID: "development", ExpectedRevision: view.Revision}
	plan, err := plans.BuildPrepare(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	req := application.SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: "first-request"}
	first, err := service.Submit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Submit(ctx, req)
	if err != nil || first.ID != second.ID {
		t.Fatalf("idempotence: %v", err)
	}
	finished := waitTask(t, service, first.ID, task.Succeeded)
	if executor.calls.Load() != 1 || finished.LastEventSeq == "0" {
		t.Fatal("duplicate effect or missing durable events")
	}
	events, err := service.Events(ctx, first.ID, "")
	if err != nil || len(events) < 3 {
		t.Fatalf("events: %v %v", events, err)
	}
	req.Fingerprint = "different"
	if _, err := service.Submit(ctx, req); err == nil || err.Error() != "REQUEST_CONFLICT" {
		t.Fatalf("request ID reused with different body: %v", err)
	}
	doc.Name = "new revision"
	if _, err := projects.Save(ctx, application.SaveProjectRequest{RegisterProjectRequest: application.RegisterProjectRequest{Document: doc}, ExpectedRevision: view.Revision}); err != nil {
		t.Fatal(err)
	}
	req.RequestID = "stale-request"
	req.Fingerprint = plan.Fingerprint
	if _, err := service.Submit(ctx, req); err == nil || err.Error() != "PLAN_STALE" {
		t.Fatalf("stale plan accepted: %v", err)
	}
	if executor.calls.Load() != 1 {
		t.Fatal("stale plan executed")
	}
}

type checkpointExecutor struct {
	entered   chan struct{}
	release   chan struct{}
	completed atomic.Bool
}

func (e *checkpointExecutor) Execute(ctx context.Context, plan task.Plan, control ports.TaskControl) error {
	close(e.entered)
	// This models a wait-policy operation: cancellation must wait for its safe point.
	<-e.release
	e.completed.Store(true)
	return control.Checkpoint(ctx)
}

func TestRecoveryAndSafeCancellation(t *testing.T) {
	ws, projects := services(t)
	ctx := context.Background()
	doc := declaration(t, "11111111-1111-4111-8111-111111111111")
	view, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc})
	if err != nil {
		t.Fatal(err)
	}
	plans := application.NewPlanService(ws, application.NewCheckService(ws, &boundedProbe{}, noFiles{}, nil))
	executor := &checkpointExecutor{entered: make(chan struct{}), release: make(chan struct{})}
	service := application.NewTaskService(ws, plans, map[task.Kind]ports.TaskExecutor{task.Prepare: executor})
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	plan, err := plans.BuildPrepare(ctx, application.ProfileRef{ProjectID: application.ProjectID(doc.ProjectID), ProfileID: "development", ExpectedRevision: view.Revision})
	if err != nil {
		t.Fatal(err)
	}
	viewTask, err := service.Submit(ctx, application.SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: "cancel-request"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("executor did not start")
	}
	cancelled, err := service.Cancel(ctx, viewTask.ID)
	if err != nil || cancelled.State != task.CancelRequested || executor.completed.Load() {
		t.Fatalf("unsafe cancel: %+v %v", cancelled, err)
	}
	close(executor.release)
	waitTask(t, service, viewTask.ID, task.Cancelled)
	if !executor.completed.Load() {
		t.Fatal("wait operation interrupted before checkpoint")
	}
}

type waitingExecutor struct {
	firstProject string
	calls        atomic.Int32
}

func (e *waitingExecutor) Execute(ctx context.Context, plan task.Plan, control ports.TaskControl) error {
	e.calls.Add(1)
	if string(plan.ProfileRef.ProjectID) == e.firstProject && control.ResumeAction() == "" {
		return &task.WaitingError{Code: "SYNTHETIC_WAIT", Message: "Waiting at a safe point."}
	}
	return control.Checkpoint(ctx)
}

func TestWaitingTaskResumesSameIdentityAndReservesResources(t *testing.T) {
	ws, projects := services(t)
	ctx := context.Background()
	firstID := "11111111-1111-4111-8111-111111111111"
	secondID := "22222222-2222-4222-8222-222222222222"
	for _, id := range []string{firstID, secondID} {
		if _, err := projects.Register(ctx, application.RegisterProjectRequest{Document: declaration(t, id)}); err != nil {
			t.Fatal(err)
		}
	}
	plans := application.NewPlanService(ws, application.NewCheckService(ws, &boundedProbe{}, noFiles{}, nil))
	executor := &waitingExecutor{firstProject: firstID}
	service := application.NewTaskService(ws, plans, map[task.Kind]ports.TaskExecutor{task.Prepare: executor})
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	submit := func(id, request string) application.TaskView {
		plan, err := plans.BuildPrepare(ctx, application.ProfileRef{ProjectID: application.ProjectID(id), ProfileID: "development", ExpectedRevision: 1})
		if err != nil {
			t.Fatal(err)
		}
		view, err := service.Submit(ctx, application.SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: request})
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	first := submit(firstID, "waiting-first")
	waiting := waitTask(t, service, first.ID, task.WaitingUser)
	second := submit(secondID, "waiting-second")
	view, err := service.Get(ctx, second.ID)
	if err != nil || view.State != task.Queued || executor.calls.Load() != 1 {
		t.Fatal("conflicting queued task bypassed waiting reservation", err)
	}
	if _, err := service.Resolve(ctx, application.ResolveTaskRequest{TaskID: first.ID, ExpectedEventSeq: "0", Action: "retry"}); err == nil || err.Error() != "TASK_CONFLICT" {
		t.Fatal("stale resolve accepted", err)
	}
	resumed, err := service.Resolve(ctx, application.ResolveTaskRequest{TaskID: first.ID, ExpectedEventSeq: waiting.LastEventSeq, Action: "retry"})
	if err != nil || resumed.ID != first.ID {
		t.Fatal("resume changed task identity", err)
	}
	waitTask(t, service, first.ID, task.Succeeded)
	waitTask(t, service, second.ID, task.Succeeded)
}
