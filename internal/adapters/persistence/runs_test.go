package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

func TestRunSnapshotCannotBeRewrittenAndProgressIsOptimistic(t *testing.T) {
	ctx := context.Background()
	repo, err := NewFactory().Initialize(ctx, t.TempDir(), "runs")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	store := repo.(*Store)
	snapshot := run.Snapshot{
		ID: "11111111-1111-4111-8111-111111111111",
		ProfileRef: project.Ref{
			ProjectID:        "22222222-2222-4222-8222-222222222222",
			ProfileID:        "development",
			ExpectedRevision: 1,
		},
		ScriptBytes: map[string][]byte{"stop": []byte("original")},
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.CreateRun(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.ScriptBytes["stop"] = []byte("changed")
	if err := store.CreateRun(ctx, snapshot); err == nil {
		t.Fatal("rewrote immutable run")
	}
	got, err := store.Run(ctx, snapshot.ID)
	if err != nil || string(got.Snapshot.ScriptBytes["stop"]) != "original" {
		t.Fatalf("lost snapshot: %+v %v", got, err)
	}
	got.State = "running"
	got.Acquisitions = []run.Acquisition{{UnitID: "api", ResourceID: "api-resource", Phase: "active"}}
	if err := store.UpdateRun(ctx, got, got.Version); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRun(ctx, got, got.Version); err == nil || err.Error() != "RUN_CONFLICT" {
		t.Fatal("stale progress accepted", err)
	}
	active, err := store.ActiveRuns(ctx)
	if err != nil || len(active) != 1 || active[0].Version != 2 {
		t.Fatalf("active state: %+v %v", active, err)
	}
	active[0].State = "stopped"
	if err := store.UpdateRun(ctx, active[0], active[0].Version); err == nil {
		t.Fatal("active acquisition silently discarded")
	}
}

func TestReopenInterruptsRunsWithoutReplayingOrClaimingTheyStopped(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := NewFactory().Initialize(ctx, root, "recovery")
	if err != nil {
		t.Fatal(err)
	}
	store := repo.(*Store)
	snapshot := run.Snapshot{
		ID: "11111111-1111-4111-8111-111111111111",
		ProfileRef: project.Ref{
			ProjectID:        "22222222-2222-4222-8222-222222222222",
			ProfileID:        "development",
			ExpectedRevision: 1,
		},
		CreatedAt: time.Now().UTC(),
	}
	if err = store.CreateRun(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = NewFactory().Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	got, err := repo.(*Store).Run(ctx, snapshot.ID)
	if err != nil || got.State != "interrupted" {
		t.Fatalf("reopen claimed execution state: %+v %v", got, err)
	}
	if active, err := repo.HasActive(ctx); err != nil || !active {
		t.Fatal("uncertain local run lost its reservation", err)
	}
}
