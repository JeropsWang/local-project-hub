package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/adapters/environment"
	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func TestManagedProfileStartsAndExplicitQuitSavesBeforeExit(t *testing.T) {
	adapter, spec, root := processFixture(t, "normal")
	ctx := context.Background()
	ws := application.NewWorkspaceService(persistence.NewFactory())
	if _, err := ws.Open(ctx, root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	data, err := os.ReadFile("../../docs/contracts/examples/demo-bundle/project.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc project.Document
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc.Requirements = map[string]project.Requirement{}
	doc.Files = map[string]project.File{}
	doc.Units = map[string]project.Unit{
		"api": {
			ResourceID:     "33333333-3333-4333-8333-333333333333",
			Label:          "Synthetic API",
			Kind:           "process",
			Control:        "managed",
			DependsOn:      []string{},
			RequirementIDs: []string{},
			Start: &project.Command{
				Executable: project.Executable{
					Kind:         "binding",
					Binding:      "helper",
					RelativePath: filepath.Base(controlHelper),
				},
				Args:        []string{"--mode", "normal"},
				CWD:         project.Path{Binding: "source", RelativePath: "."},
				EnvBindings: map[string]string{},
			},
			Readiness: &project.Probe{Kind: "adapter", AdapterID: "managed-control-v1", TimeoutSeconds: 2},
			Stop:      &project.Stop{Kind: "adapter", AdapterID: "managed-control-v1", TimeoutSeconds: 2},
		},
	}
	doc.Profiles = map[string]project.Profile{
		"development": {Label: "development", UnitIDs: []string{"api"}, FeatureIDs: []string{}},
	}
	projects := application.NewProjectService(ws, contract.DecodeProject)
	view, err := projects.Register(
		ctx,
		application.RegisterProjectRequest{
			Document: doc,
			Bindings: []project.Binding{
				{Key: "source", Kind: "path", LocalLocator: spec.Command.CWD},
				{Key: "helper", Kind: "path", LocalLocator: filepath.Dir(controlHelper)},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	checks := application.NewCheckService(ws, nil, environment.StaticInspector{}, nil)
	plans := application.NewPlanService(ws, checks, application.WithRunCompiler(environment.RunCompiler{}))
	stop := application.NewStopExecutor(ws, adapter, nil)
	start := application.NewStartExecutor(
		ws,
		adapter,
		nil,
		stop,
		nil,
		nil,
		application.StartWithReadiness(environment.NewReadinessAdapter(adapter, nil, nil)),
	)
	tasks := application.NewTaskService(ws, plans, map[task.Kind]ports.TaskExecutor{task.Start: start, task.Stop: stop})
	t.Cleanup(func() { _ = tasks.Close(ctx) })
	lifecycle := application.NewLifecycleService(ws, tasks, stop)
	plan, err := plans.BuildStart(
		ctx,
		application.ProfileRef{
			ProjectID:        project.ID(doc.ProjectID),
			ProfileID:        "development",
			ExpectedRevision: view.Revision,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	started, err := tasks.Submit(
		ctx,
		application.SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: "synthetic-profile"},
	)
	if err != nil {
		t.Fatal(err)
	}
	waitIntegrationTask(t, tasks, started.ID, task.Succeeded)
	waitFile(t, filepath.Join(spec.Command.CWD, "ready"))
	if allowed, err := lifecycle.CanQuit(ctx); err != nil || allowed {
		t.Fatal("running profile allowed quit", err)
	}
	quit, err := lifecycle.RequestExit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitIntegrationTask(t, tasks, quit.ID, task.Succeeded)
	if _, err = os.Stat(filepath.Join(spec.Command.CWD, "saved.txt")); err != nil {
		t.Fatal("quit without saving", err)
	}
	if allowed, err := lifecycle.CanQuit(ctx); err != nil || !allowed {
		t.Fatal("graceful quit blocked", err)
	}
}

func waitIntegrationTask(t *testing.T, tasks *application.TaskService, id task.ID, want task.State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		view, err := tasks.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if view.State == want {
			return
		}
		if view.State == task.Failed || view.State == task.WaitingUser {
			t.Fatalf("task blocked: %+v error: %+v", view, view.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task did not reach %s", want)
}
