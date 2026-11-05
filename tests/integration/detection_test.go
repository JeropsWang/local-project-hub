package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/adapters/environment"
	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
	versions "github.com/JeropsWang/local-project-hub/internal/adapters/version"
	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type observedNode struct{ Version string }

func fixtureRuntime(t *testing.T, doc *project.Document) project.Binding {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for id, unit := range doc.Units {
		if unit.Start != nil {
			unit.Start.Executable.RelativePath = filepath.Base(self)
		}
		if unit.Stop != nil && unit.Stop.Command != nil {
			unit.Stop.Command.Executable.RelativePath = filepath.Base(self)
		}
		doc.Units[id] = unit
	}
	return project.Binding{Key: "node-runtime", Kind: "runtime", LocalLocator: self}
}

func (p observedNode) Discover(context.Context, ports.ProbeRequest) (ports.Observation, error) {
	return ports.Observation{Version: p.Version, Architecture: "amd64", Evidence: check.Evidence{Kind: "observation", Source: "synthetic-node"}}, nil
}

func TestPythonSourceMinimumCannotBeIgnored(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := t.TempDir()
	data, err := os.ReadFile("../../docs/contracts/examples/demo-bundle/project.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc project.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	python := doc.Requirements["node-runtime"]
	python.Component = "python"
	python.Semantics = "pep440"
	python.Range = ">=3.10"
	doc.Requirements["node-runtime"] = python
	delete(doc.Units, "database")
	api := doc.Units["api"]
	api.DependsOn = []string{}
	api.Start.EnvBindings = map[string]string{}
	api.Stop.Command.EnvBindings = map[string]string{}
	api.Start.Executable.RelativePath = "python.exe"
	api.Stop.Command.Executable.RelativePath = "python.exe"
	doc.Units["api"] = api
	doc.Profiles["development"] = project.Profile{Label: "dev", UnitIDs: []string{"api"}, FeatureIDs: []string{}}
	doc.Files["python-metadata"] = project.File{Availability: "external", Path: &project.Path{Binding: "source", RelativePath: "pyproject.toml"}, Role: "source", Required: true}
	if err := os.MkdirAll(filepath.Join(source, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"server.mjs": "# synthetic only\n", "scripts/stop.mjs": "# synthetic only\n", "pyproject.toml": "[project]\nname = 'synthetic'\nrequires-python = '>=3.12'\n"} {
		if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(name)), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ws := application.NewWorkspaceService(persistence.NewFactory())
	defer ws.Close()
	if _, err := ws.Initialize(ctx, application.InitWorkspaceRequest{Root: root, Name: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	projects := application.NewProjectService(ws, contract.DecodeProject)
	runtimeBinding := fixtureRuntime(t, &doc)
	view, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc, Bindings: []project.Binding{{Key: "source", Kind: "path", LocalLocator: source}, runtimeBinding}})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewCheckService(ws, observedNode{Version: "3.11.9"}, environment.StaticInspector{}, versions.Evaluate)
	report, err := service.Check(ctx, application.CheckRequest{ProfileRef: application.ProfileRef{ProjectID: application.ProjectID(doc.ProjectID), ProfileID: "development", ExpectedRevision: view.Revision}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range report.Items {
		for _, reason := range item.Reasons {
			if reason.Code == "VERSION_RANGE_MISMATCH" && strings.Contains(reason.Subject, "pyproject.toml") {
				found = true
			}
		}
	}
	if !found || report.Summary == "ready" {
		t.Fatalf("Python source minimum ignored: %+v", report)
	}
}

func TestDetectionIntersectsLockfileInsteadOfTrustingRootMinimum(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := t.TempDir()
	data, err := os.ReadFile("../../docs/contracts/examples/demo-bundle/project.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc project.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	node := doc.Requirements["node-runtime"]
	node.Range = ">=22.19.0"
	doc.Requirements["node-runtime"] = node
	delete(doc.Units, "database")
	api := doc.Units["api"]
	api.DependsOn = []string{}
	api.Start.EnvBindings = map[string]string{}
	api.Stop.Command.EnvBindings = map[string]string{}
	doc.Units["api"] = api
	profile := doc.Profiles["development"]
	profile.UnitIDs = []string{"api"}
	doc.Profiles["development"] = profile
	if err := os.MkdirAll(filepath.Join(source, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"server.mjs", "scripts/stop.mjs"} {
		if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(name)), []byte("throw new Error('never execute');\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	lock := `{"lockfileVersion":3,"packages":{"":{"engines":{"node":">=22.19.0"}},"node_modules/vitest":{"engines":{"node":"^22.12.0 || ^24.0.0 || >=26.0.0"}},"node_modules/darwin-only":{"os":["darwin"],"engines":{"node":">=99.0.0"}}}}`
	if err := os.WriteFile(filepath.Join(source, "package-lock.json"), []byte(lock), 0600); err != nil {
		t.Fatal(err)
	}
	ws := application.NewWorkspaceService(persistence.NewFactory())
	defer ws.Close()
	if _, err := ws.Initialize(ctx, application.InitWorkspaceRequest{Root: root, Name: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	projects := application.NewProjectService(ws, contract.DecodeProject)
	runtimeBinding := fixtureRuntime(t, &doc)
	view, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc, Bindings: []project.Binding{{Key: "source", Kind: "path", LocalLocator: source}, runtimeBinding}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ version, want string }{{"22.19.0", "ready"}, {"23.0.0", "not-ready"}, {"24.0.0", "ready"}, {"25.0.0", "not-ready"}} {
		service := application.NewCheckService(ws, observedNode{Version: test.version}, environment.StaticInspector{}, versions.Evaluate)
		report, err := service.Check(ctx, application.CheckRequest{ProfileRef: application.ProfileRef{ProjectID: application.ProjectID(doc.ProjectID), ProfileID: "development", ExpectedRevision: view.Revision}})
		if err != nil || report.Summary != test.want {
			t.Errorf("Node %s: summary %s, expected %s, error %v", test.version, report.Summary, test.want, err)
		}
		for _, item := range report.Items {
			for _, e := range item.Evidence {
				if e.Kind == "runtime-test" || e.Kind == "desktop-test" {
					t.Fatal("static check fabricated runtime evidence")
				}
			}
		}
	}
}
