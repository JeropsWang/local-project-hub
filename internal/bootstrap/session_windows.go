package bootstrap

import (
	"context"
	"os/exec"

	"github.com/JeropsWang/local-project-hub/internal/adapters/archive"
	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/adapters/docker"
	"github.com/JeropsWang/local-project-hub/internal/adapters/environment"
	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
	"github.com/JeropsWang/local-project-hub/internal/adapters/process"
	"github.com/JeropsWang/local-project-hub/internal/adapters/version"
	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/desktop/services"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// captureFactory retains the repository capability for native adapters while
// the workspace service remains responsible for owning the database lock.
type captureFactory struct {
	ports.WorkspaceFactory
	repo ports.Repository
}

func (f *captureFactory) Initialize(ctx context.Context, root, name string) (ports.Repository, error) {
	repo, err := f.WorkspaceFactory.Initialize(ctx, root, name)
	f.repo = repo
	return repo, err
}

func (f *captureFactory) Open(ctx context.Context, root string) (ports.Repository, error) {
	repo, err := f.WorkspaceFactory.Open(ctx, root)
	f.repo = repo
	return repo, err
}

func newSession(ctx context.Context, req application.InitWorkspaceRequest, create bool) (*services.Session, error) {
	factory := &captureFactory{WorkspaceFactory: persistence.NewFactory()}
	ws := application.NewWorkspaceService(factory)
	var view application.WorkspaceView
	var err error
	if create {
		view, err = ws.Initialize(ctx, req)
	} else {
		view, err = ws.Open(ctx, req.Root)
	}
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = factory.repo.Close()
		}
	}()
	catalog, err := environment.DefaultRuntimeCatalog()
	if err != nil {
		return nil, err
	}
	supply, err := environment.NewRuntimeProvider(view.Root, catalog, nil)
	if err != nil {
		return nil, err
	}
	preparer, err := environment.NewProjectPreparer(view.Root, process.NewPreparationRunner(view.Root))
	if err != nil {
		return nil, err
	}
	processes, err := process.NewProcessAdapter(view.Root)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !success {
			_ = processes.Close()
		}
	}()
	var compose ports.ComposeRunner
	if executable, lookupErr := exec.LookPath("docker.exe"); lookupErr == nil {
		compose, err = docker.NewComposeRunner(executable, process.NewToolRunner())
		if err != nil {
			return nil, err
		}
	}
	containers, err := docker.NewContainerAdapter(view.Root, map[string]string{
		"desktop": `\\.\pipe\dockerDesktopLinuxEngine`,
	}, factory.repo.(ports.ContainerAuthorization), compose)
	if err != nil {
		return nil, err
	}
	resources := application.NewResourceService(ws, containers, containers)
	checks := application.NewCheckService(ws, environment.NewProbe(), environment.StaticInspector{}, version.Evaluate)
	plans := application.NewPlanService(ws, checks, application.WithRuntimeProvider(supply),
		application.WithProjectPreparer(preparer), application.WithRunCompiler(environment.RunCompiler{}))
	bundles := application.NewBundleService(ws, archive.Writer{}, archive.Reader{})
	stop := application.NewStopExecutor(ws, processes, resources)
	ready := environment.NewReadinessAdapter(processes, containers, factory.repo.(ports.ResourceRepository))
	start := application.NewStartExecutor(ws, processes, containers, stop, nil, process.NewUnitRunner(view.Root), application.StartWithReadiness(ready))
	tasks := application.NewTaskService(ws, plans, map[task.Kind]ports.TaskExecutor{
		task.Prepare: application.NewPrepareExecutor(ws, supply, application.PrepareProjectWith(preparer)),
		task.Start:   start, task.Stop: stop, task.Import: bundles, task.Export: bundles,
	})
	lifecycle := application.NewLifecycleService(ws, tasks, stop)
	success = true
	return &services.Session{Workspace: ws, View: view, Projects: application.NewProjectService(ws, contract.DecodeProject),
		Checks: checks, Plans: plans, Tasks: tasks, Lifecycle: lifecycle, Bundles: bundles,
		Containers: containers, Resources: factory.repo.(ports.ResourceRepository)}, nil
}
