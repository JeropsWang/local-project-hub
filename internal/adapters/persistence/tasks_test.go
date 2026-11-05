package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func TestTaskEventsAndRequestIdentitySurviveReopen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := NewFactory().Initialize(ctx, root, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if repo != nil {
			_ = repo.Close()
		}
	})
	store := repo.(ports.TaskRepository)
	plan := task.Plan{
		ID:          "11111111-1111-4111-8111-111111111111",
		Kind:        task.Prepare,
		WorkspaceID: repo.Info().WorkspaceID,
		Fingerprint: "synthetic-plan",
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	record := task.Record{
		View:        task.View{ID: "22222222-2222-4222-8222-222222222222", Kind: task.Prepare},
		PlanID:      plan.ID,
		RequestID:   "request",
		RequestHash: "fixed-request-hash",
		CreatedAt:   time.Now().UTC(),
	}
	created, err := store.CreateTask(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := store.CreateTask(ctx, record)
	if err != nil || duplicate.View.ID != created.View.ID {
		t.Fatal("duplicate durable task", err)
	}
	if _, err := store.TransitionTask(ctx, created.View.ID, created.View.LastEventSeq, task.Running, nil, task.EventData{Code: "RUNNING"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = NewFactory().Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	store = repo.(ports.TaskRepository)
	count, err := store.RecoverTasks(ctx)
	if err != nil || count != 1 {
		t.Fatalf("recovery: %d %v", count, err)
	}
	got, err := store.FindTaskRequest(ctx, "request")
	if err != nil || got.View.State != task.Interrupted {
		t.Fatalf("uncertain task replayed or lost: %+v %v", got, err)
	}
	events, err := store.TaskEvents(ctx, got.View.ID, "1")
	if err != nil || len(events) != 2 || events[0].Seq != "2" || events[1].Seq != "3" {
		t.Fatalf("event replay: %+v %v", events, err)
	}
}

func TestResolveWaitingTaskChangesActionAndStateAtomically(t *testing.T) {
	ctx := context.Background()
	repo, err := NewFactory().Initialize(ctx, t.TempDir(), "resolve")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	store := repo.(ports.TaskRepository)
	created, err := store.CreateTask(
		ctx,
		task.Record{
			View:        task.View{ID: "11111111-1111-4111-8111-111111111111", Kind: task.Exit},
			PlanID:      "plan",
			RequestID:   "resolve",
			RequestHash: "body",
			CreatedAt:   time.Now().UTC(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	running, err := store.TransitionTask(
		ctx,
		created.View.ID,
		created.View.LastEventSeq,
		task.Running,
		nil,
		task.EventData{Code: "RUNNING"},
	)
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := store.TransitionTask(
		ctx,
		running.View.ID,
		running.View.LastEventSeq,
		task.WaitingUser,
		nil,
		task.EventData{Code: "WAITING"},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolveTask(ctx, waiting.View.ID, waiting.View.LastEventSeq, "continue-wait")
	if err != nil || resolved.View.State != task.Queued || resolved.ResumeAction != "continue-wait" {
		t.Fatalf("resolve not committed together: %+v %v", resolved, err)
	}
	if _, err = store.ResolveTask(ctx, waiting.View.ID, waiting.View.LastEventSeq, "abort-exit"); err == nil ||
		err.Error() != "TASK_CONFLICT" {
		t.Fatal("old resolve overwrote action", err)
	}
	latest, err := store.Task(ctx, created.View.ID)
	if err != nil || latest.ResumeAction != "continue-wait" {
		t.Fatal("failed resolve changed action", err)
	}
	if latest.View.LastEventSeq != "4" {
		t.Fatal("resolve published a partial intermediate action", latest.View.LastEventSeq)
	}
}
