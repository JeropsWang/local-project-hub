package filesystem_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
)

func TestWorkspaceIdentityRejectsDuplicateKeys(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	factory := persistence.NewFactory()
	store, err := factory.Initialize(ctx, root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "workspace.json")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"schemaVersion": "1.0"`), []byte(`"schemaVersion":"1.0","schemaVersion":"1.0"`), 1)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if opened, err := factory.Open(ctx, root); err == nil {
		opened.Close()
		t.Fatal("duplicate workspace identity keys accepted")
	}
}

func TestWorkspaceRejectsNonemptyAndNetworkRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	factory := persistence.NewFactory()
	if _, err := factory.Initialize(context.Background(), root, "test"); err == nil {
		t.Fatal("nonempty directory reused")
	}
	if _, err := factory.Initialize(context.Background(), `\\server\share\workspace`, "test"); err == nil || err.Error() != "WORKSPACE_PATH_UNSUPPORTED" {
		t.Fatalf("network root accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "workspace.db")); !os.IsNotExist(err) {
		t.Fatal("failed init left database")
	}
}

func TestMovedWorkspaceRequiresRebinding(t *testing.T) {
	parent := t.TempDir()
	first := filepath.Join(parent, "first")
	second := filepath.Join(parent, "second")
	factory := persistence.NewFactory()
	store, err := factory.Initialize(context.Background(), first, "test")
	if err != nil {
		t.Fatal(err)
	}
	id := store.Info().WorkspaceID
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(first, second); err != nil {
		t.Fatal(err)
	}
	store, err = factory.Open(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.Info().State != "rebind-required" || store.Info().WorkspaceID != id {
		t.Fatal("relocated workspace trusted stale paths")
	}
}
