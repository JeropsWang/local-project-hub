package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
)

func TestSaveConflictPreservesPreviouslyExistingRevisionDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := NewFactory().Initialize(ctx, root, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	data, err := os.ReadFile("../../../docs/contracts/examples/demo-bundle/project.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := contract.DecodeProject(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Register(ctx, doc, data, nil, nil); err != nil {
		t.Fatal(err)
	}
	conflict := filepath.Join(root, "projects", doc.ProjectID, "revisions", "2", "user-owned.txt")
	if err := os.MkdirAll(filepath.Dir(conflict), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflict, []byte("preserve me"), 0600); err != nil {
		t.Fatal(err)
	}
	doc.Name = "updated"
	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, doc, 1, data, nil, nil); err == nil {
		t.Fatal("conflict was overwritten")
	}
	if value, err := os.ReadFile(conflict); err != nil || string(value) != "preserve me" {
		t.Fatalf("failed save deleted preexisting content: %v", err)
	}
}

func TestPublishedRevisionRecoversAfterTransactionFailure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := NewFactory().Initialize(ctx, root, "test")
	if err != nil {
		t.Fatal(err)
	}
	store := repo.(*Store)
	data, err := os.ReadFile("../../../docs/contracts/examples/demo-bundle/project.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := contract.DecodeProject(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Register(ctx, doc, data, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_revision BEFORE INSERT ON project_revisions WHEN NEW.revision=2 BEGIN SELECT RAISE(ABORT,'synthetic failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, doc, 1, data, nil, nil); err == nil {
		t.Fatal("injected transaction failure ignored")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = NewFactory().Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	got, err := repo.Get(ctx, doc.ProjectID)
	if err != nil || got.Revision != 1 {
		t.Fatal("failed transaction replaced prior revision", err)
	}
	if _, err := os.Stat(filepath.Join(root, "projects", doc.ProjectID, "revisions", "2")); !os.IsNotExist(err) {
		t.Fatalf("published orphan not recovered: %v", err)
	}
}

func TestLegacyWorkspaceUpgradeRequiresEmptyRecoveryJournal(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := NewFactory().Initialize(ctx, root, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	store := repo.(*Store)
	if _, err := store.db.Exec("ALTER TABLE revision_journal DROP COLUMN owner_token; UPDATE workspace_meta SET db_schema_version=1;"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = NewFactory().Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Info().DBSchemaVersion != 3 {
		t.Fatal("legacy manager schema was not explicitly upgraded")
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
}

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
	owner := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(filepath.Join(orphan, ".lph-revision-owner"), []byte(owner), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO revision_journal(project_id, revision, relative_path,owner_token) VALUES(?,1,?,?)", id, rel, owner); err != nil {
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
