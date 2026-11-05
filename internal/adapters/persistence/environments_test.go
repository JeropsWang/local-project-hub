package persistence

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/domain/preparation"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

// A storage failure on the second binding must roll back the first selection too.
func TestEnvironmentActivationRollsBackAllBindingsOnDatabaseFailure(t *testing.T) {
	ctx := context.Background()
	root, original := t.TempDir(), t.TempDir()
	repo, err := NewFactory().Initialize(ctx, root, "environment activation")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	store := repo.(*Store)
	data, err := os.ReadFile("../../../docs/contracts/examples/demo-bundle/project.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := contract.DecodeProject(data)
	if err != nil {
		t.Fatal(err)
	}
	old := project.Binding{Key: "source", Kind: "path", LocalLocator: original}
	if _, err := repo.Register(ctx, doc, data, []project.Binding{old}, nil); err != nil {
		t.Fatal(err)
	}
	envRoot := filepath.Join(root, "envs", doc.ProjectID, "development", "fixture")
	copyRoot := filepath.Join(envRoot, "sources", "source")
	sdkRoot := filepath.Join(root, "runtimes", "node", "fixture")
	for _, dir := range []string{copyRoot, sdkRoot} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	program := filepath.Join(sdkRoot, "node.exe")
	if err := os.WriteFile(program, []byte("fixture runtime; never executed"), 0600); err != nil {
		t.Fatal(err)
	}
	receipt := preparation.Receipt{WorkspaceID: repo.Info().WorkspaceID, Ref: project.Ref{ProjectID: project.ID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 1}, Root: envRoot, State: "ready", PlanFingerprint: "fixture", OriginalBindings: []project.Binding{old}, Bindings: []project.Binding{{Key: "source", Kind: "path", LocalLocator: copyRoot}, {Key: "node-runtime", Kind: "runtime", LocalLocator: program}}}
	marker, _ := json.Marshal(receipt)
	if err := os.WriteFile(filepath.Join(envRoot, ".lph-environment.json"), marker, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER reject_runtime BEFORE INSERT ON bindings WHEN NEW.binding_key='node-runtime' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateEnvironment(ctx, receipt); err == nil {
		t.Fatal("database failure ignored")
	}
	bound, err := repo.Binding(ctx, doc.ProjectID, "source")
	if err != nil || bound != old {
		t.Fatal("partial selection committed", err)
	}
	if _, err := repo.Binding(ctx, doc.ProjectID, "node-runtime"); err == nil || err.Error() != "BINDING_MISSING" {
		t.Fatal("runtime selection partially committed")
	}
	if _, err := store.db.ExecContext(ctx, "DROP TRIGGER reject_runtime"); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateEnvironment(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	bound, err = repo.Binding(ctx, doc.ProjectID, "source")
	if err != nil || bound.LocalLocator != copyRoot {
		t.Fatal("complete selection not committed", err)
	}
}
