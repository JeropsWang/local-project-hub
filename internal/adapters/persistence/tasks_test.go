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
	plan := task.Plan{ID: "11111111-1111-4111-8111-111111111111", Kind: task.Prepare, WorkspaceID: repo.Info().WorkspaceID, Fingerprint: "synthetic-plan", CreatedAt: time.Now().UTC()}
	if err := store.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	record := task.Record{View: task.View{ID: "22222222-2222-4222-8222-222222222222", Kind: task.Prepare}, PlanID: plan.ID, RequestID: "request", RequestHash: "fixed-request-hash", CreatedAt: time.Now().UTC()}
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
