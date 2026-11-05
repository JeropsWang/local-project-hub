package application_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/adapters/environment"
	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type preparedRuntime struct {
	root        string
	fingerprint string
	installs    atomic.Int32
}

type fixtureRestore struct{ fail bool }

func (r fixtureRestore) Run(ctx context.Context, command ports.PreparationCommand) (ports.PreparationResult, error) {
	if r.fail {
		return ports.PreparationResult{}, problem.New("DEPENDENCY_RESTORE_FAILED", "")
	}
	return ports.PreparationResult{}, os.WriteFile(filepath.Join(command.CWD, "restored.txt"), []byte("managed dependencies"), 0600)
}

// The source/runtime selection must commit together only after the full restore succeeds.
func TestProjectPreparationActivatesOnlyCompleteEnvironment(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			ctx := context.Background()
			root, source := t.TempDir(), t.TempDir()
			ws := application.NewWorkspaceService(persistence.NewFactory())
			t.Cleanup(func() { _ = ws.Close() })
			if _, err := ws.Initialize(ctx, application.InitWorkspaceRequest{Root: root, Name: "project copy"}); err != nil {
				t.Fatal(err)
			}
			projects := application.NewProjectService(ws, contract.DecodeProject)
			doc := declaration(t, "11111111-1111-4111-8111-111111111111")
			if err := os.MkdirAll(filepath.Join(source, "scripts"), 0700); err != nil {
				t.Fatal(err)
			}
			for name, contents := range map[string]string{"package.json": `{"name":"fixture","version":"1.0.0"}`, "package-lock.json": `{"lockfileVersion":3,"packages":{}}`, "server.mjs": "console.log('fixture')", "scripts/stop.mjs": "console.log('stop fixture')"} {
				if err := os.WriteFile(filepath.Join(source, name), []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			view, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc, Bindings: []project.Binding{{Key: "source", Kind: "path", LocalLocator: source}}})
			if err != nil {
				t.Fatal(err)
			}
			runtimeRoot := filepath.Join(root, "runtimes", "node", "fixture")
			if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(runtimeRoot, "node.exe"), []byte("synthetic runtime; not executed"), 0600); err != nil {
				t.Fatal(err)
			}
			provider := &preparedRuntime{root: runtimeRoot, fingerprint: "pinned-sdk"}
			preparer, err := environment.NewProjectPreparer(root, fixtureRestore{fail: fail})
			if err != nil {
				t.Fatal(err)
			}
			plans := application.NewPlanService(ws, application.NewCheckService(ws, &boundedProbe{}, noFiles{}, nil), application.WithRuntimeProvider(provider), application.WithProjectPreparer(preparer))
			plan, err := plans.BuildPrepare(ctx, application.ProfileRef{ProjectID: application.ProjectID(doc.ProjectID), ProfileID: "development", ExpectedRevision: view.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if plan.ProjectPreparation == nil {
				t.Fatal("copy/install effects missing from preview")
			}
			executor := application.NewPrepareExecutor(ws, provider, application.PrepareProjectWith(preparer))
			service := application.NewTaskService(ws, plans, map[task.Kind]ports.TaskExecutor{task.Prepare: executor})
			t.Cleanup(func() { _ = service.Close(ctx) })
			job, err := service.Submit(ctx, application.SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: "copy-and-prepare"})
			if err != nil {
				t.Fatal(err)
			}
			wanted := task.Succeeded
			if fail {
				wanted = task.Failed
			}
			waitTask(t, service, job.ID, wanted)
			bound, err := ws.Binding(ctx, application.ProjectID(doc.ProjectID), "source")
			if err != nil {
				t.Fatal(err)
			}
			if fail && bound.LocalLocator != source {
				t.Fatal("failed restore activated source")
			}
			if !fail {
				if bound.LocalLocator == source {
					t.Fatal("successful restore not selected")
				}
				if got, err := os.ReadFile(filepath.Join(bound.LocalLocator, "restored.txt")); err != nil || string(got) != "managed dependencies" {
					t.Fatal("incomplete copy selected", err)
				}
			}
			_, runtimeErr := ws.Binding(ctx, application.ProjectID(doc.ProjectID), "node-runtime")
			if fail && (runtimeErr == nil || runtimeErr.Error() != "BINDING_MISSING") {
				t.Fatal("failed restore changed runtime selection")
			}
			if !fail && runtimeErr != nil {
				t.Fatal(runtimeErr)
			}
			after, err := projects.Get(ctx, application.ProjectID(doc.ProjectID))
			if err != nil || after.Revision != view.Revision || after.ContentHash != view.ContentHash {
				t.Fatal("portable revision changed", err)
			}
			if _, err := os.Stat(filepath.Join(source, "restored.txt")); !os.IsNotExist(err) {
				t.Fatal("original source modified")
			}
		})
	}
}

func (p *preparedRuntime) Plan(context.Context, ports.RuntimeRequest) (ports.RuntimeInstallPlan, error) {
	return ports.RuntimeInstallPlan{ArtifactID: "synthetic-node", Component: "node", Version: "22.19.0", Architecture: "amd64", Destination: p.root, Fingerprint: p.fingerprint}, nil
}
func (p *preparedRuntime) Install(context.Context, ports.RuntimeInstallPlan) (ports.RuntimeLocation, error) {
	p.installs.Add(1)
	return ports.RuntimeLocation{Root: p.root, Executable: filepath.Join(p.root, "node.exe"), Component: "node", Version: "22.19.0", Architecture: "amd64", Fingerprint: p.fingerprint}, nil
}

func TestPreparationBindsRuntimeWithoutChangingPortableRevision(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := application.NewWorkspaceService(persistence.NewFactory())
	t.Cleanup(func() { _ = ws.Close() })
	if _, err := ws.Initialize(ctx, application.InitWorkspaceRequest{Root: root, Name: "prepare"}); err != nil {
		t.Fatal(err)
	}
	projects := application.NewProjectService(ws, contract.DecodeProject)
	doc := declaration(t, "11111111-1111-4111-8111-111111111111")
	view, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc})
	if err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(root, "runtimes", "node", "fixture")
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "node.exe"), []byte("synthetic executable; never run"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := &preparedRuntime{root: runtimeRoot, fingerprint: "artifact-v1"}
	plans := application.NewPlanService(ws, application.NewCheckService(ws, &boundedProbe{}, noFiles{}, nil), application.WithRuntimeProvider(provider))
	service := application.NewTaskService(ws, plans, map[task.Kind]ports.TaskExecutor{task.Prepare: application.NewPrepareExecutor(ws, provider)})
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	plan, err := plans.BuildPrepare(ctx, application.ProfileRef{ProjectID: application.ProjectID(doc.ProjectID), ProfileID: "development", ExpectedRevision: view.Revision})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Submit(ctx, application.SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: "prepare-sdk"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, service, job.ID, task.Succeeded)
	bound, err := ws.Binding(ctx, application.ProjectID(doc.ProjectID), "node-runtime")
	if err != nil || bound.Kind != "runtime" || bound.LocalLocator != filepath.Join(runtimeRoot, "node.exe") {
		t.Fatalf("runtime binding: %+v %v", bound, err)
	}
	after, err := projects.Get(ctx, application.ProjectID(doc.ProjectID))
	if err != nil || after.Revision != view.Revision || after.ContentHash != view.ContentHash || provider.installs.Load() != 1 {
		t.Fatal("preparation mutated portable revision", err)
	}
}

func TestChangedRuntimeSupplyInvalidatesPreparationPlan(t *testing.T) {
	ws, projects := services(t)
	ctx := context.Background()
	doc := declaration(t, "11111111-1111-4111-8111-111111111111")
	if _, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc, Bindings: []project.Binding{}}); err != nil {
		t.Fatal(err)
	}
	provider := &preparedRuntime{root: t.TempDir(), fingerprint: "artifact-v1"}
	plans := application.NewPlanService(ws, application.NewCheckService(ws, &boundedProbe{}, noFiles{}, nil), application.WithRuntimeProvider(provider))
	plan, err := plans.BuildPrepare(ctx, application.ProfileRef{ProjectID: application.ProjectID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	provider.fingerprint = "artifact-v2"
	service := application.NewTaskService(ws, plans, map[task.Kind]ports.TaskExecutor{task.Prepare: application.NewPrepareExecutor(ws, provider)})
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	if _, err := service.Submit(ctx, application.SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: "changed-supply"}); err == nil || err.Error() != "PLAN_STALE" {
		t.Fatalf("changed supply accepted: %v", err)
	}
	if provider.installs.Load() != 0 {
		t.Fatal("changed artifact executed")
	}
}
