package application_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

func services(t *testing.T) (*application.WorkspaceService, *application.ProjectService) {
	t.Helper()
	root := t.TempDir()
	workspaces := application.NewWorkspaceService(persistence.NewFactory())
	t.Cleanup(func() { _ = workspaces.Close() })
	_, err := workspaces.Initialize(context.Background(), application.InitWorkspaceRequest{Root: root, Name: "测试工作空间"})
	if err != nil {
		t.Fatal(err)
	}
	return workspaces, application.NewProjectService(workspaces, contract.DecodeProject)
}

func declaration(t *testing.T, id string) project.Document {
	t.Helper()
	data, err := os.ReadFile("../../docs/contracts/examples/demo-bundle/project.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc project.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc.ProjectID = id
	return doc
}

func TestSameBindingKeyIsScopedByProject(t *testing.T) {
	workspaces, projects := services(t)
	ctx := context.Background()
	ids := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	for i, id := range ids {
		_, err := projects.Register(ctx, application.RegisterProjectRequest{Document: declaration(t, id), Bindings: []project.Binding{{Key: "source", Kind: "path", LocalLocator: filepath.Join(t.TempDir(), string(rune('a'+i)))}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	a, err := workspaces.Binding(ctx, application.ProjectID(ids[0]), "source")
	if err != nil {
		t.Fatal(err)
	}
	b, err := workspaces.Binding(ctx, application.ProjectID(ids[1]), "source")
	if err != nil || a.LocalLocator == b.LocalLocator {
		t.Fatalf("bindings not scoped: %v", err)
	}
	if _, err := projects.Register(ctx, application.RegisterProjectRequest{Document: declaration(t, ids[0])}); err == nil || err.Error() != "PROJECT_EXISTS" {
		t.Fatalf("duplicate overwrote project: %v", err)
	}
}

func TestSaveRevisionConflictAndCrashRecovery(t *testing.T) {
	_, projects := services(t)
	ctx := context.Background()
	doc := declaration(t, "11111111-1111-4111-8111-111111111111")
	doc.Files["local-script"] = project.File{Availability: "included", BundlePath: "files/start.ps1", Required: true, Role: "script"}
	first, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc, IncludedFiles: map[string][]byte{"files/start.ps1": []byte("Write-Output 'original'\n")}})
	if err != nil {
		t.Fatal(err)
	}
	doc.Name = "已修改"
	second, err := projects.Save(ctx, application.SaveProjectRequest{RegisterProjectRequest: application.RegisterProjectRequest{Document: doc, IncludedFiles: map[string][]byte{"files/start.ps1": []byte("Write-Output 'new'\n")}}, ExpectedRevision: first.Revision})
	if err != nil || second.Revision != 2 {
		t.Fatalf("save: %v", err)
	}
	if _, err := projects.Save(ctx, application.SaveProjectRequest{RegisterProjectRequest: application.RegisterProjectRequest{Document: doc}, ExpectedRevision: first.Revision}); err == nil || err.Error() != "REVISION_CONFLICT" {
		t.Fatalf("lost concurrent update: %v", err)
	}
	got, err := projects.Get(ctx, application.ProjectID(doc.ProjectID))
	if err != nil || got.Revision != 2 || got.Document.Name != "已修改" {
		t.Fatalf("wrong revision: %+v %v", got, err)
	}
}

func TestRegisterRejectsMissingFilesAndPlainSecret(t *testing.T) {
	_, projects := services(t)
	doc := declaration(t, "11111111-1111-4111-8111-111111111111")
	doc.Files["local-script"] = project.File{Availability: "included", BundlePath: "files/start.ps1", Required: true, Role: "script"}
	if _, err := projects.Register(context.Background(), application.RegisterProjectRequest{Document: doc}); err == nil {
		t.Fatal("missing included file accepted")
	}
	delete(doc.Files, "local-script")
	if _, err := projects.Register(context.Background(), application.RegisterProjectRequest{Document: doc, Bindings: []project.Binding{{Key: "database-secret", Kind: "secret", LocalLocator: "not-a-real-secret"}}}); err == nil {
		t.Fatal("plaintext secret persisted as locator")
	}
}

func TestActiveRunPreventsWorkspaceSwitch(t *testing.T) {
	root := t.TempDir()
	ws := application.NewWorkspaceService(persistence.NewFactory())
	view, err := ws.Initialize(context.Background(), application.InitWorkspaceRequest{Root: root, Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer func() { _, _ = db.Exec("DELETE FROM runs"); _ = ws.Close() }()
	if _, err := db.Exec("INSERT INTO runs VALUES('test-run','test-project','development',1,'{}','{}','running')"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Initialize(context.Background(), application.InitWorkspaceRequest{Root: filepath.Join(t.TempDir(), "next"), Name: "next"}); err == nil || err.Error() != "WORKSPACE_BUSY" {
		t.Fatalf("active workspace detached: %v", err)
	}
	if view.WorkspaceID == "" {
		t.Fatal("identity missing")
	}
}

func TestRelocationConfirmationClearsMachineBindings(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	before := filepath.Join(parent, "before")
	after := filepath.Join(parent, "after")
	ws := application.NewWorkspaceService(persistence.NewFactory())
	view, err := ws.Initialize(ctx, application.InitWorkspaceRequest{Root: before, Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	projects := application.NewProjectService(ws, contract.DecodeProject)
	id := "11111111-1111-4111-8111-111111111111"
	if _, err := projects.Register(ctx, application.RegisterProjectRequest{Document: declaration(t, id), Bindings: []project.Binding{{Key: "source", Kind: "path", LocalLocator: before}}}); err != nil {
		t.Fatal(err)
	}
	if err := ws.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(before, after); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Open(ctx, after); err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if _, err := ws.ConfirmRelocation(ctx, application.WorkspaceID("wrong-workspace")); err == nil {
		t.Fatal("wrong workspace confirmed")
	}
	confirmed, err := ws.ConfirmRelocation(ctx, application.WorkspaceID(view.WorkspaceID))
	if err != nil || confirmed.State != "ready" {
		t.Fatalf("cannot complete relocation: %v", err)
	}
	if _, err := ws.Binding(ctx, application.ProjectID(id), "source"); err == nil {
		t.Fatal("old machine binding survived relocation")
	}
	if got, err := projects.Get(ctx, application.ProjectID(id)); err != nil || got.CheckSummary != "unverified" {
		t.Fatal("portable project lost or trusted")
	}
}
