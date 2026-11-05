package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/environment"
	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
	nativeprocess "github.com/JeropsWang/local-project-hub/internal/adapters/process"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// TestPinnedRuntimeSupplySmoke is explicit opt-in: normal CI never downloads SDKs.
// It creates only the development repository's ignored, manager-owned smoke workspace.
func TestPinnedRuntimeSupplySmoke(t *testing.T) {
	if os.Getenv("LPH_RUNTIME_SUPPLY_SMOKE") != "1" {
		t.Skip("set LPH_RUNTIME_SUPPLY_SMOKE=1 to verify pinned public SDK distributions")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(repoRoot, ".superpowers", "runtime-smoke")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if _, err := os.Stat(filepath.Join(root, "workspace.json")); os.IsNotExist(err) {
		repo, err := persistence.NewFactory().Initialize(ctx, root, "Development runtime smoke")
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := environment.DefaultRuntimeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	provider, err := environment.NewRuntimeProvider(root, catalog, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range catalog.Artifacts {
		t.Run(artifact.ID, func(t *testing.T) {
			semantics, rangeText := "exact", artifact.Version
			if artifact.Component == "python" {
				semantics = "pep440"
				rangeText = "==" + artifact.Version
			}
			if artifact.Component == "go" {
				semantics = "goversion"
				rangeText = artifact.Version
			}
			plan, err := provider.Plan(
				ctx,
				ports.RuntimeRequest{
					Requirement: project.Requirement{
						Component:        artifact.Component,
						Semantics:        semantics,
						Range:            rangeText,
						PreferredVersion: artifact.Version,
						Architecture:     "amd64",
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			location, err := provider.Install(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := environment.NewProbe().
				Discover(ctx, ports.ProbeRequest{Tool: artifact.Component, Executable: location.Executable})
			if err != nil || observed.Version != artifact.Version || observed.Architecture != "amd64" {
				t.Fatalf("pinned executable smoke: %+v %v", observed, err)
			}
			t.Logf(
				"verified %s %s %s; SHA256 %s",
				artifact.Component,
				observed.Version,
				observed.Architecture,
				artifact.SHA256,
			)
			reused, err := provider.Plan(
				ctx,
				ports.RuntimeRequest{
					Requirement: project.Requirement{
						Component:    artifact.Component,
						Semantics:    semantics,
						Range:        rangeText,
						Architecture: "amd64",
					},
				},
			)
			if err != nil || !reused.AlreadyInstalled {
				t.Fatalf("read-only probe mutated installed SDK: %+v %v", reused, err)
			}
		})
	}
}

// TestManagedProjectPreparationSmoke restores synthetic empty projects with the
// actual pinned SDKs. No original project, database or WeChat process is involved.
func TestManagedProjectPreparationSmoke(t *testing.T) {
	if os.Getenv("LPH_PROJECT_PREPARATION_SMOKE") != "1" {
		t.Skip("set LPH_PROJECT_PREPARATION_SMOKE=1 for isolated SDK/project preparation")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(repoRoot, ".superpowers", "runtime-smoke")
	catalog, err := environment.DefaultRuntimeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	provider, err := environment.NewRuntimeProvider(root, catalog, nil)
	if err != nil {
		t.Fatal(err)
	}
	preparer, err := environment.NewProjectPreparer(root, nativeprocess.NewPreparationRunner(root))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, artifact := range catalog.Artifacts {
		t.Run(artifact.ID, func(t *testing.T) {
			semantics, rangeText := "exact", artifact.Version
			if artifact.Component == "python" {
				semantics, rangeText = "pep440", "=="+artifact.Version
			}
			if artifact.Component == "go" {
				semantics = "goversion"
			}
			requirement := project.Requirement{
				Component:    artifact.Component,
				Semantics:    semantics,
				Range:        rangeText,
				Required:     true,
				Architecture: "amd64",
			}
			sdkPlan, err := provider.Plan(ctx, ports.RuntimeRequest{Requirement: requirement})
			if err != nil {
				t.Fatal(err)
			}
			sdk, err := provider.Install(ctx, sdkPlan)
			if err != nil {
				t.Fatal(err)
			}
			source := t.TempDir()
			files := map[string]string{}
			switch artifact.Component {
			case "node":
				files["package.json"] = `{"name":"fixture","version":"1.0.0","scripts":{"postinstall":"node -e \"require('fs').writeFileSync('installed.txt','managed-only')\""}}`
				files["package-lock.json"] = `{"name":"fixture","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"fixture","version":"1.0.0","hasInstallScript":true}}}`
			case "python":
				files["requirements.txt"] = "# synthetic empty dependencies\n"
			case "go":
				files["go.mod"] = "module example.invalid/fixture\n\ngo 1.26.1\n"
			}
			for name, value := range files {
				if err := os.WriteFile(filepath.Join(source, name), []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			doc := project.Document{
				Requirements: map[string]project.Requirement{"sdk": requirement},
				Units: map[string]project.Unit{
					"app": {
						RequirementIDs: []string{"sdk"},
						Start:          &project.Command{CWD: project.Path{Binding: "source", RelativePath: "."}},
					},
				},
			}
			plan, err := preparer.Plan(
				ctx,
				ports.ProjectPreparationRequest{
					Ref: project.Ref{
						ProjectID:        "99999999-9999-4999-8999-999999999999",
						ProfileID:        "smoke",
						ExpectedRevision: 1,
					},
					Document: doc,
					UnitIDs:  []string{"app"},
					Bindings: map[string]project.Binding{
						"source": {Key: "source", Kind: "path", LocalLocator: source},
					},
					RuntimePlans: map[string]ports.RuntimeInstallPlan{"sdk": sdkPlan},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := preparer.Prepare(ctx, plan, map[string]ports.RuntimeLocation{"sdk": sdk})
			if err != nil {
				t.Fatal(err)
			}
			copyRoot := receipt.Bindings[0].LocalLocator
			if artifact.Component == "node" {
				if data, err := os.ReadFile(filepath.Join(copyRoot, "installed.txt")); err != nil ||
					string(data) != "managed-only" {
					t.Fatal("npm lifecycle did not use managed copy", err)
				}
				if _, err := os.Stat(filepath.Join(source, "installed.txt")); !os.IsNotExist(err) {
					t.Fatal("npm wrote original")
				}
			}
			if artifact.Component == "python" {
				locator := receipt.Bindings[1].LocalLocator
				if locator != filepath.Join(copyRoot, ".venv", "Scripts", "python.exe") {
					t.Fatal("venv not selected at its permanent location")
				}
				observation, err := environment.NewProbe().
					Discover(ctx, ports.ProbeRequest{Tool: "python", Executable: locator})
				if err != nil || observation.Version != artifact.Version {
					t.Fatalf("venv ABI smoke: %+v %v", observation, err)
				}
				if _, err := os.Stat(filepath.Join(source, ".venv")); !os.IsNotExist(err) {
					t.Fatal("original venv created")
				}
			}
			t.Logf(
				"prepared synthetic %s %s: %d restore steps; original unchanged",
				artifact.Component,
				artifact.Version,
				len(plan.Restores),
			)
			reused, err := provider.Plan(ctx, ports.RuntimeRequest{Requirement: requirement})
			if err != nil || !reused.AlreadyInstalled {
				t.Fatalf("preparation mutated shared SDK: %+v %v", reused, err)
			}
		})
	}
}
