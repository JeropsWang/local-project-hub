// Package services exposes application use cases to the desktop runtime.
package services

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// Session contains one workspace's active services and native lifetime.
type Session struct {
	Workspace      *application.WorkspaceService
	Projects       *application.ProjectService
	Checks         *application.CheckService
	Plans          *application.PlanService
	Tasks          *application.TaskService
	Lifecycle      *application.LifecycleService
	Bundles        *application.BundleService
	Containers     ports.ContainerAdapter
	Resources      ports.ResourceRepository
	CloseProcesses func() error
	View           application.WorkspaceView
}

type SessionFactory func(context.Context, application.InitWorkspaceRequest, bool) (*Session, error)

type Manager struct {
	mu      sync.RWMutex
	factory SessionFactory
	session *Session
}

func NewManager(factory SessionFactory) *Manager { return &Manager{factory: factory} }

// ServiceShutdown runs after the native quit gate has allowed Wails to exit.
func (m *Manager) ServiceShutdown() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.session.Tasks.Close(ctx); err != nil {
		return err
	}
	err := errors.Join(m.session.CloseProcesses(), m.session.Workspace.Close())
	m.session = nil
	return err
}

// SelectWorkspace binds the desktop to one workspace for its current lifetime.
func (m *Manager) SelectWorkspace(ctx context.Context, req application.InitWorkspaceRequest, create bool) (application.WorkspaceView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session != nil {
		return application.WorkspaceView{}, problem.New("WORKSPACE_ALREADY_OPEN", "")
	}
	session, err := m.factory(ctx, req, create)
	if err != nil {
		return application.WorkspaceView{}, err
	}
	m.session = session
	return session.View, nil
}

func (m *Manager) current() (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.session == nil {
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	return m.session, nil
}

func (m *Manager) Workspace() *application.WorkspaceView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.session == nil {
		return nil
	}
	view := m.session.View
	return &view
}

// ConfirmRelocation reopens project editing after the core revokes host bindings.
func (m *Manager) ConfirmRelocation(ctx context.Context, id application.WorkspaceID) (application.WorkspaceView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session == nil {
		return application.WorkspaceView{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	view, err := m.session.Workspace.ConfirmRelocation(ctx, id)
	if err == nil {
		m.session.View = view
	}
	return view, err
}

func (m *Manager) Projects(ctx context.Context) ([]application.ProjectView, error) {
	session, err := m.current()
	if err != nil {
		return nil, err
	}
	return session.Projects.List(ctx, application.ProjectQuery{Limit: 100})
}

func (m *Manager) Register(ctx context.Context, req application.RegisterProjectRequest) (application.ProjectView, error) {
	session, err := m.current()
	if err != nil {
		return application.ProjectView{}, err
	}
	return session.Projects.Register(ctx, req)
}

func (m *Manager) Save(ctx context.Context, req application.SaveProjectRequest) (application.ProjectView, error) {
	session, err := m.current()
	if err != nil {
		return application.ProjectView{}, err
	}
	return session.Projects.Save(ctx, req)
}

func (m *Manager) Check(ctx context.Context, ref application.ProfileRef) (application.CheckReport, error) {
	session, err := m.current()
	if err != nil {
		return application.CheckReport{}, err
	}
	return session.Checks.Check(ctx, application.CheckRequest{ProfileRef: ref})
}
