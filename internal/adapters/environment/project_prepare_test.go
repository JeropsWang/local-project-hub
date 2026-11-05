package environment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func copyRequest(source string) ports.ProjectPreparationRequest {
	return ports.ProjectPreparationRequest{
		Ref: project.Ref{
			ProjectID:        "11111111-1111-4111-8111-111111111111",
			ProfileID:        "development",
			ExpectedRevision: 1,
		},
		Document: project.Document{
			Units: map[string]project.Unit{
				"app": {
					Kind:  "process",
					Start: &project.Command{CWD: project.Path{Binding: "source", RelativePath: "."}},
				},
			},
		},
		UnitIDs: []string{
			"app",
		}, Bindings: map[string]project.Binding{"source": {Key: "source", Kind: "path", LocalLocator: source}},
	}
}

func putSource(t *testing.T, root, name, contents string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

// A copied inherited venv/.deps or a write to the original would break this test.
func TestManagedSourceCopyExcludesInheritedEnvironmentsAndSecrets(t *testing.T) {
	ctx := context.Background()
	root, source := runtimeWorkspace(t), t.TempDir()
	for name, value := range map[string]string{"app.py": "print('hello')", ".venv/keep": "old venv", ".deps/keep": "old ABI", "node_modules/keep": "old packages", ".env": "FAKE_SECRET=fixture", "data/keep.db": "original data", ".env.example": "TOKEN=", ".git/config": "local git config"} {
		putSource(t, source, name, value)
	}
	preparer, err := NewProjectPreparer(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := preparer.Plan(ctx, copyRequest(source))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := preparer.Prepare(ctx, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	copyRoot := receipt.Bindings[0].LocalLocator
	if got, err := os.ReadFile(filepath.Join(copyRoot, "app.py")); err != nil || string(got) != "print('hello')" {
		t.Fatal("source missing", err)
	}
	for _, name := range []string{".venv", ".deps", "node_modules", ".env", "data", ".git"} {
		if _, err := os.Stat(filepath.Join(copyRoot, name)); !os.IsNotExist(err) {
			t.Fatalf("inherited input copied: %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(copyRoot, ".env.example")); err != nil {
		t.Fatal("template excluded", err)
	}
	if got, err := os.ReadFile(filepath.Join(source, ".venv", "keep")); err != nil || string(got) != "old venv" {
		t.Fatal("original environment changed")
	}
	if receipt.PlanFingerprint != plan.Fingerprint || receipt.State != "ready" ||
		receipt.OriginalBindings[0].LocalLocator != source {
		t.Fatalf("missing local provenance: %+v", receipt)
	}
}

// Adding or changing even an undeclared source file after preview must stale the copy plan.
func TestSourceCopyDetectsChangesAndPreservesForeignEnvironment(t *testing.T) {
	ctx := context.Background()
	root, source := runtimeWorkspace(t), t.TempDir()
	putSource(t, source, "app.py", "before")
	preparer, err := NewProjectPreparer(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := preparer.Plan(ctx, copyRequest(source))
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "envs", "foreign")
	putSource(t, foreign, "keep.txt", "foreign")
	putSource(t, source, "new.py", "changed since preview")
	if _, err := preparer.Prepare(ctx, plan, nil); err == nil || err.Error() != "SOURCE_PLAN_STALE" {
		t.Fatalf("changed source accepted: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(foreign, "keep.txt")); err != nil || string(got) != "foreign" {
		t.Fatal("foreign copy changed")
	}
}

type failingRestore struct {
	cause      error
	copiedRoot string
}

func (r *failingRestore) Run(ctx context.Context, cmd ports.PreparationCommand) (ports.PreparationResult, error) {
	r.copiedRoot = cmd.OwnedRoot
	return ports.PreparationResult{}, r.cause
}

// Restore failure must remove only the new disposable copy and leave original and prior copies intact.
func TestDependencyRestoreFailureDoesNotActivateOrLeakCopy(t *testing.T) {
	ctx := context.Background()
	root, source := runtimeWorkspace(t), t.TempDir()
	putSource(t, source, "package.json", `{"name":"fixture","version":"1.0.0"}`)
	putSource(
		t,
		source,
		"package-lock.json",
		`{"name":"fixture","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"fixture","version":"1.0.0"}}}`,
	)
	putSource(t, source, "node_modules/keep", "original")
	runner := &failingRestore{cause: context.Canceled}
	preparer, err := NewProjectPreparer(root, runner)
	if err != nil {
		t.Fatal(err)
	}
	req := copyRequest(source)
	req.Document.Requirements = map[string]project.Requirement{"node-runtime": {Component: "node", Required: true}}
	req.Document.Units["app"] = project.Unit{
		RequirementIDs: []string{"node-runtime"},
		Start:          req.Document.Units["app"].Start,
	}
	req.RuntimePlans = map[string]ports.RuntimeInstallPlan{"node-runtime": {Component: "node", Fingerprint: "pinned"}}
	plan, err := preparer.Plan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Restores) == 0 || plan.Restores[0].Effect != "package-install-scripts" {
		t.Fatal("install effects hidden")
	}
	_, err = preparer.Prepare(
		ctx,
		plan,
		map[string]ports.RuntimeLocation{
			"node-runtime": {
				Root:        filepath.Join(root, "runtimes", "node", "fixture"),
				Executable:  filepath.Join(root, "runtimes", "node", "fixture", "node.exe"),
				Fingerprint: "pinned",
			},
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("restore failure lost: %v", err)
	}
	if runner.copiedRoot == "" {
		t.Fatal("restore not attempted")
	}
	if _, err := os.Stat(runner.copiedRoot); !os.IsNotExist(err) {
		t.Fatal("failed copy leaked")
	}
	if got, err := os.ReadFile(filepath.Join(source, "node_modules", "keep")); err != nil || string(got) != "original" {
		t.Fatal("original dependencies changed")
	}
}

func TestRequiredExcludedFileAndUnpinnedNodeRestoreAreReported(t *testing.T) {
	root, source := runtimeWorkspace(t), t.TempDir()
	putSource(t, source, ".env", "FAKE=fixture")
	preparer, err := NewProjectPreparer(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := copyRequest(source)
	req.Document.Files = map[string]project.File{
		"config": {
			Required:     true,
			Availability: "external",
			Path:         &project.Path{Binding: "source", RelativePath: ".env"},
		},
	}
	if _, err := preparer.Plan(context.Background(), req); err == nil ||
		err.Error() != "SOURCE_REQUIRED_FILE_EXCLUDED" {
		t.Fatalf("silently dropped declared config: %v", err)
	}
	delete(req.Document.Files, "config")
	putSource(t, source, "package.json", `{"name":"fixture"}`)
	req.Document.Requirements = map[string]project.Requirement{"node-runtime": {Component: "node", Required: true}}
	u := req.Document.Units["app"]
	u.RequirementIDs = []string{"node-runtime"}
	req.Document.Units["app"] = u
	req.RuntimePlans = map[string]ports.RuntimeInstallPlan{"node-runtime": {Component: "node", Fingerprint: "pinned"}}
	if _, err := preparer.Plan(context.Background(), req); err == nil || err.Error() != "NODE_LOCKFILE_REQUIRED" {
		t.Fatalf("unlocked dependency restore accepted: %v", err)
	}
}

func TestUnselectedOptionalPythonRequirementsDoNotSuppressPrimaryVenv(t *testing.T) {
	root, source := runtimeWorkspace(t), t.TempDir()
	putSource(t, source, "app.py", "print('primary')")
	putSource(t, source, "optional-model/requirements.txt", "optional-fixture-package==1.0")
	preparer, err := NewProjectPreparer(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := copyRequest(source)
	req.Document.Requirements = map[string]project.Requirement{"python-runtime": {Component: "python", Required: true}}
	u := req.Document.Units["app"]
	u.RequirementIDs = []string{"python-runtime"}
	req.Document.Units["app"] = u
	req.RuntimePlans = map[string]ports.RuntimeInstallPlan{
		"python-runtime": {Component: "python", Fingerprint: "pinned"},
	}
	plan, err := preparer.Plan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Restores) != 2 || plan.Restores[0].Effect != "create-venv" ||
		plan.Restores[0].Directory.RelativePath != "." ||
		plan.Restores[1].Effect != "verify-dependencies" {
		t.Fatalf("unselected optional packages affected primary environment: %+v", plan.Restores)
	}
}
