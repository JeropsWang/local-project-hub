package persistence

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestRejectNewerDatabaseAndDuplicateOpen(t *testing.T) {
	ctx := context.Background()
	factory := NewFactory()
	root := t.TempDir()
	store, err := factory.Initialize(ctx, root, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := factory.Open(ctx, root); err == nil || err.Error() != "WORKSPACE_BUSY" {
		t.Fatalf("second writer accepted: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE workspace_meta SET db_schema_version=999"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := factory.Open(ctx, root); err == nil || err.Error() != "WORKSPACE_VERSION_UNSUPPORTED" {
		t.Fatalf("newer DB accepted: %v", err)
	}
}

func TestRecoveryOnlyRemovesJournaledOrphans(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	factory := NewFactory()
	store, err := factory.Initialize(ctx, root, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(root, "user-owned.txt")
	if err := os.WriteFile(unknown, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after publishing this tool's journaled revision directory.
	id := "11111111-1111-4111-8111-111111111111"
	rel := filepath.ToSlash(filepath.Join("projects", id, "revisions", "1"))
	orphan := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(orphan, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "owned.marker"), []byte("journaled"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO revision_journal(project_id, revision, relative_path) VALUES(?,1,?)", id, rel); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = factory.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan not recovered: %v", err)
	}
	if data, err := os.ReadFile(unknown); err != nil || string(data) != "keep" {
		t.Fatal("unrelated file changed")
	}
}
