package application

import (
	"context"
	"sync"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// WorkspaceService prevents a workspace switch during a repository operation.
type WorkspaceService struct {
	mu      sync.RWMutex
	factory ports.WorkspaceFactory
	current ports.Repository
}

// NewWorkspaceService accepts a factory; the core does not know SQLite or Windows.
func NewWorkspaceService(factory ports.WorkspaceFactory) *WorkspaceService {
	return &WorkspaceService{factory: factory}
}

// Initialize creates an empty, explicitly chosen workspace without installing SDKs.
func (s *WorkspaceService) Initialize(ctx context.Context, req InitWorkspaceRequest) (WorkspaceView, error) {
	return s.switchTo(ctx, req.Root, req.Name, true)
}

// Open verifies identity and exposes relocation as a pending rebind state.
func (s *WorkspaceService) Open(ctx context.Context, root string) (WorkspaceView, error) {
	return s.switchTo(ctx, root, "", false)
}

func (s *WorkspaceService) switchTo(ctx context.Context, root, name string, initialize bool) (WorkspaceView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		active, err := s.current.HasActive(ctx)
		if err != nil {
			return WorkspaceView{}, err
		}
		if active {
			return WorkspaceView{}, problem.New("WORKSPACE_BUSY", "")
		}
		if !initialize && s.current.Info().Root == root {
			return s.current.Info(), nil
		}
	}
	var next ports.Repository
	var err error
	if initialize {
		next, err = s.factory.Initialize(ctx, root, name)
	} else {
		next, err = s.factory.Open(ctx, root)
	}
	if err != nil {
		return WorkspaceView{}, err
	}
	if s.current != nil {
		if err := s.current.Close(); err != nil {
			_ = next.Close()
			return WorkspaceView{}, err
		}
	}
	s.current = next
	return next.Info(), nil
}

// Close releases only manager storage and locks; callers must coordinate shutdown first.
func (s *WorkspaceService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil
	}
	if active, err := s.current.HasActive(context.Background()); err != nil {
		return err
	} else if active {
		return problem.New("WORKSPACE_BUSY", "")
	}
	err := s.current.Close()
	s.current = nil
	return err
}

// Binding reads a project-scoped locator/reference; it never decrypts a secret.
func (s *WorkspaceService) Binding(ctx context.Context, id ProjectID, key string) (project.Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current == nil {
		return project.Binding{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	return s.current.Binding(ctx, string(id), key)
}

// ConfirmRelocation accepts a moved workspace while invalidating machine authority.
// Each project must then bind and check its target environment again.
func (s *WorkspaceService) ConfirmRelocation(ctx context.Context, id WorkspaceID) (WorkspaceView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return WorkspaceView{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	return s.current.ConfirmRelocation(ctx, string(id))
}
