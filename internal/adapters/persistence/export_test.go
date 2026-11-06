package persistence

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

func TestMissingOrChangedIncludedFileBlocksExport(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := NewFactory().Initialize(ctx, root, "export")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data, err := os.ReadFile("../../../docs/contracts/examples/demo-bundle/project.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := contract.DecodeProject(data)
	if err != nil {
		t.Fatal(err)
	}
	doc.Files["script"] = project.File{
		Availability: "included",
		BundlePath:   "files/start.ps1",
		Role:         "script",
		Required:     true,
	}
	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Register(ctx, doc, data, nil, map[string][]byte{"files/start.ps1": []byte("fixture")}); err != nil {
		t.Fatal(err)
	}
	store := repo.(*Store)
	ref := project.Ref{ProjectID: project.ID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 1}
	if _, err = store.ExportRevision(ctx, ref); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "projects", doc.ProjectID, "revisions", "1", "files", "start.ps1")
	if err = os.WriteFile(name, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ExportRevision(ctx, ref); err == nil || err.Error() != "REVISION_FILE_CHANGED" {
		t.Fatal("changed bytes accepted", err)
	}
	if err = os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ExportRevision(ctx, ref); err == nil || err.Error() != "BUNDLE_FILE_MISSING" {
		t.Fatal("missing bytes accepted", err)
	}
}
