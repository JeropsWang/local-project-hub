package application

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

// ProjectService persists only declarations that pass the full injected contract decoder.
type ProjectService struct {
	workspaces *WorkspaceService
	decode     func([]byte) (project.Document, error)
}

// NewProjectService wires validation without a dependency on the contract adapter.
func NewProjectService(workspaces *WorkspaceService, decode func([]byte) (project.Document, error)) *ProjectService {
	return &ProjectService{workspaces: workspaces, decode: decode}
}

func (s *ProjectService) validate(req RegisterProjectRequest) ([]byte, error) {
	data, err := json.Marshal(req.Document)
	if err != nil {
		return nil, problem.New("PROJECT_INVALID", "")
	}
	if _, err = s.decode(data); err != nil {
		return nil, err
	}
	if err = validateBindings(req.Bindings); err != nil {
		return nil, err
	}
	return data, nil
}

func validateBindings(bindings []project.Binding) error {
	keys := map[string]bool{}
	for _, binding := range bindings {
		if !bindingKey.MatchString(binding.Key) || keys[binding.Key] {
			return problem.New("BINDING_INVALID", "")
		}
		keys[binding.Key] = true
		if binding.Kind == "secret" {
			if binding.SecretRef == "" || binding.LocalLocator != "" {
				return problem.New("BINDING_SECRET_REFERENCE_REQUIRED", "")
			}
		} else if binding.Kind != "path" && binding.Kind != "resource" && binding.Kind != "runtime" {
			return problem.New("BINDING_KIND_INVALID", "")
		} else if binding.LocalLocator == "" || binding.SecretRef != "" {
			return problem.New("BINDING_INVALID", "")
		}
	}
	return nil
}

var bindingKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// Register creates revision 1 and never overwrites an existing project identity.
func (s *ProjectService) Register(ctx context.Context, req RegisterProjectRequest) (ProjectView, error) {
	data, err := s.validate(req)
	if err != nil {
		return ProjectView{}, err
	}
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	store := s.workspaces.current
	if store == nil {
		return ProjectView{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	if store.Info().State != "ready" {
		return ProjectView{}, problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	return store.Register(ctx, req.Document, data, req.Bindings, req.IncludedFiles)
}

// Save creates the next immutable revision; a stale editor cannot replace new changes.
func (s *ProjectService) Save(ctx context.Context, req SaveProjectRequest) (ProjectView, error) {
	data, err := s.validate(req.RegisterProjectRequest)
	if err != nil {
		return ProjectView{}, err
	}
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	store := s.workspaces.current
	if store == nil {
		return ProjectView{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	if store.Info().State != "ready" {
		return ProjectView{}, problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	return store.Save(ctx, req.Document, req.ExpectedRevision, data, req.Bindings, req.IncludedFiles)
}

// Get returns a current configuration snapshot, never secret values.
func (s *ProjectService) Get(ctx context.Context, id ProjectID) (ProjectView, error) {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	if s.workspaces.current == nil {
		return ProjectView{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	return s.workspaces.current.Get(ctx, string(id))
}

// List returns at most 100 entries in deterministic library order.
func (s *ProjectService) List(ctx context.Context, query ProjectQuery) ([]ProjectView, error) {
	if query.Limit < 1 || query.Limit > 100 || query.Offset < 0 {
		return nil, problem.New("PROJECT_QUERY_INVALID", "")
	}
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	if s.workspaces.current == nil {
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	return s.workspaces.current.List(ctx, query)
}
