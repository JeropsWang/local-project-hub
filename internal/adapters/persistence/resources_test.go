package persistence

import (
	"context"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func grantedResourceFixture() resource.Record {
	id := run.Identity{
		Kind:         "container",
		Engine:       "desktop",
		EngineID:     "fixture-engine",
		ContainerID:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreationTime: "created",
		StartedAt:    "started",
		ControlID:    "33333333-3333-4333-8333-333333333333",
	}
	return resource.Record{
		SchemaVersion:     "1.0",
		ResourceID:        "22222222-2222-4222-8222-222222222222",
		Control:           "managed",
		Engine:            id.Engine,
		EngineID:          id.EngineID,
		ComposeProject:    "existing-stack",
		WorkingDir:        `D:\fixture`,
		Files:             []resource.File{{Path: `D:\fixture\compose.yml`, SHA256: "fixture-input"}},
		Services:          []string{"database"},
		ConfigFingerprint: "fixture-config",
		Instances:         []run.Identity{id},
		ControlGrant:      id.ControlID,
		StopAttempts:      map[string]string{},
	}
}

func TestContainerGrantAndStopJournalSurviveReopenAndRevoke(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := NewFactory().Initialize(ctx, root, "resource journal")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if repo != nil {
			_ = repo.Close()
		}
	})
	store := repo.(*Store)
	record, err := store.SaveResource(ctx, grantedResourceFixture(), "")
	if err != nil {
		t.Fatal(err)
	}
	instance := record.Instances[0]
	if err := store.AuthorizeContainer(ctx, instance); err != nil {
		t.Fatal(err)
	}
	wrong := instance
	wrong.StartedAt = "different start"
	if err := store.AuthorizeContainer(ctx, wrong); err == nil {
		t.Fatal("grant accepted another container generation")
	}
	first, err := store.BeginContainerStop(ctx, instance, "SIGTERM")
	if err != nil || !first {
		t.Fatal("initial delivery not journalled", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo = nil
	repo, err = NewFactory().Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	store = repo.(*Store)
	first, err = store.BeginContainerStop(ctx, instance, "SIGTERM")
	if err != nil || first {
		t.Fatal("restart would resend uncertain stop signal", err)
	}
	if _, err := store.BeginContainerStop(ctx, instance, "SIGINT"); err == nil ||
		err.Error() != "CONTAINER_STOP_SIGNAL_CHANGED" {
		t.Fatal("changed signal repeated previous action", err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE resources SET control_grant='',fingerprint='' WHERE resource_id=?", record.ResourceID); err != nil {
		t.Fatal(err)
	}
	if err := store.AuthorizeContainer(ctx, instance); err == nil || err.Error() != "RESOURCE_CONTROL_NOT_GRANTED" {
		t.Fatal("stale JSON authority survived revocation", err)
	}
}

func TestComposeGrantDoesNotAuthorizeAnotherWorkingDirectory(t *testing.T) {
	ctx := context.Background()
	repo, err := NewFactory().Initialize(ctx, t.TempDir(), "source grant")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	store := repo.(*Store)
	record, err := store.SaveResource(ctx, grantedResourceFixture(), "")
	if err != nil {
		t.Fatal(err)
	}
	spec := ports.ComposeSpec{
		ResourceID:        record.ResourceID,
		Engine:            record.Engine,
		ExpectedEngineID:  record.EngineID,
		ProjectName:       record.ComposeProject,
		ControlGrant:      record.ControlGrant,
		WorkingDir:        `D:\other-source`,
		Files:             []ports.ComposeFile{{Path: record.Files[0].Path, SHA256: record.Files[0].SHA256}},
		Services:          record.Services,
		ConfigFingerprint: record.ConfigFingerprint,
	}
	if err := store.AuthorizeCompose(ctx, spec); err == nil || err.Error() != "RESOURCE_CONTROL_NOT_GRANTED" {
		t.Fatalf("grant escaped its reviewed cwd: %v", err)
	}
}
