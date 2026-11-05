package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/environment"
	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
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
			plan, err := provider.Plan(ctx, ports.RuntimeRequest{Requirement: project.Requirement{Component: artifact.Component, Semantics: semantics, Range: rangeText, PreferredVersion: artifact.Version, Architecture: "amd64"}})
			if err != nil {
				t.Fatal(err)
			}
			location, err := provider.Install(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := environment.NewProbe().Discover(ctx, ports.ProbeRequest{Tool: artifact.Component, Executable: location.Executable})
			if err != nil || observed.Version != artifact.Version || observed.Architecture != "amd64" {
				t.Fatalf("pinned executable smoke: %+v %v", observed, err)
			}
			t.Logf("verified %s %s %s; SHA256 %s", artifact.Component, observed.Version, observed.Architecture, artifact.SHA256)
		})
	}
}
