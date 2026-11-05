package application

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// ReferenceResourceRequest links observed instances without granting start/stop control.
type ReferenceResourceRequest struct {
	ResourceID          string
	Engine              string
	ContainerIDs        []string
	ExpectedFingerprint string
	Shared              bool
}

// ManagedResourceRequest is the explicit local grant action, separate from importing a UUID.
// Existing stacks must be deliberately selected and match their running source evidence.
type ManagedResourceRequest struct {
	Spec                ports.ComposeSpec
	Shared              bool
	AdoptExisting       bool
	ExpectedFingerprint string
}

// ResourceStopSummary reports why a shared/reference resource was kept running.
type ResourceStopSummary struct {
	Stopped bool                        `json:"stopped"`
	Reason  string                      `json:"reason"`
	Results []ports.ContainerStopResult `json:"results"`
}

// ResourceService organizes local bindings/consumers. Mutations run through the
// workspace's serialized task executor; a project name is never an authority source.
type ResourceService struct {
	workspaces *WorkspaceService
	containers ports.ContainerAdapter
	planner    ports.ComposePlanner
}

// NewResourceService depends only on local storage and explicit container capabilities.
func NewResourceService(
	ws *WorkspaceService,
	containers ports.ContainerAdapter,
	planner ports.ComposePlanner,
) *ResourceService {
	return &ResourceService{workspaces: ws, containers: containers, planner: planner}
}

func (s *ResourceService) repository() (ports.ResourceRepository, error) {
	if s.workspaces.current == nil {
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	repo, ok := s.workspaces.current.(ports.ResourceRepository)
	if !ok {
		return nil, problem.New("RESOURCE_STORAGE_UNAVAILABLE", "")
	}
	return repo, nil
}

func (s *ResourceService) readyRepository() (ports.ResourceRepository, error) {
	repo, err := s.repository()
	if err != nil {
		return nil, err
	}
	if s.workspaces.current.Info().State != "ready" {
		return nil, problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	return repo, nil
}

// Get returns local relationship state without probing or adopting anything.
func (s *ResourceService) Get(ctx context.Context, id string) (resource.Record, error) {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo, err := s.repository()
	if err != nil {
		return resource.Record{}, err
	}
	return repo.Resource(ctx, id)
}

// RegisterReference stores only the exact selected observed instances and no grant.
func (s *ResourceService) RegisterReference(
	ctx context.Context,
	req ReferenceResourceRequest,
) (resource.Record, error) {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo, err := s.readyRepository()
	if err != nil {
		return resource.Record{}, err
	}
	if len(req.ContainerIDs) == 0 || len(req.ContainerIDs) > 10000 {
		return resource.Record{}, problem.New("RESOURCE_SELECTION_REQUIRED", "")
	}
	observed, err := s.containers.Inventory(ctx, req.Engine)
	if err != nil {
		return resource.Record{}, err
	}
	record := resource.Record{
		SchemaVersion:         "1.0",
		ResourceID:            req.ResourceID,
		Control:               "reference",
		Engine:                req.Engine,
		Shared:                req.Shared,
		Users:                 []resource.Use{},
		StopAttempts:          map[string]string{},
		ContainerConfigHashes: map[string]string{},
	}
	seen := map[string]bool{}
	for _, id := range req.ContainerIDs {
		if seen[id] {
			return resource.Record{}, problem.New("RESOURCE_SELECTION_INVALID", "")
		}
		seen[id] = true
		index := slices.IndexFunc(
			observed,
			func(item ports.ContainerObservation) bool { return item.Identity.ContainerID == id },
		)
		if index < 0 {
			return resource.Record{}, problem.New("CONTAINER_INSTANCE_MISSING", id)
		}
		item := observed[index]
		if record.EngineID != "" && record.EngineID != item.Identity.EngineID {
			return resource.Record{}, problem.New("DOCKER_ENGINE_CHANGED", "")
		}
		record.EngineID = item.Identity.EngineID
		item.Identity.ControlID = ""
		record.Instances = append(record.Instances, item.Identity)
		record.ContainerConfigHashes[id] = item.ConfigHash
	}
	return repo.SaveResource(ctx, record, req.ExpectedFingerprint)
}

// GrantManaged validates source/engine/project/service evidence before creating a
// fresh local grant. It does not start containers or modify Compose source files.
func (s *ResourceService) GrantManaged(ctx context.Context, req ManagedResourceRequest) (resource.Record, error) {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo, err := s.readyRepository()
	if err != nil {
		return resource.Record{}, err
	}
	spec := req.Spec
	if spec.ProjectName == "" {
		spec.ProjectName, err = resource.ComposeProject(spec.ResourceID)
		if err != nil {
			return resource.Record{}, err
		}
	}
	preview, err := s.planner.PreviewCompose(ctx, spec)
	if err != nil {
		return resource.Record{}, err
	}
	spec = preview.Spec
	if spec.ExpectedEngineID == "" || preview.Fingerprint == "" {
		return resource.Record{}, problem.New("RESOURCE_PREVIEW_INVALID", "")
	}
	observed, err := s.containers.Inventory(ctx, spec.Engine)
	if err != nil {
		return resource.Record{}, err
	}
	for _, item := range observed {
		if item.Identity.EngineID != spec.ExpectedEngineID {
			return resource.Record{}, problem.New("DOCKER_ENGINE_CHANGED", "")
		}
		if item.ComposeProject == spec.ProjectName && !req.AdoptExisting {
			return resource.Record{}, problem.New("RESOURCE_EXISTING_REQUIRES_REVIEW", spec.ResourceID)
		}
	}
	grant, err := identity.NewUUID()
	if err != nil {
		return resource.Record{}, err
	}
	record := resource.Record{
		SchemaVersion:         "1.0",
		ResourceID:            spec.ResourceID,
		Control:               "managed",
		Engine:                spec.Engine,
		EngineID:              spec.ExpectedEngineID,
		ComposeProject:        spec.ProjectName,
		WorkingDir:            spec.WorkingDir,
		Services:              slices.Clone(spec.Services),
		ConfigFingerprint:     preview.Fingerprint,
		Shared:                req.Shared,
		ControlGrant:          grant,
		Users:                 []resource.Use{},
		StopAttempts:          map[string]string{},
		ContainerConfigHashes: map[string]string{},
	}
	expectedFiles := []string{}
	for _, file := range spec.Files {
		record.Files = append(record.Files, resource.File{Path: file.Path, SHA256: file.SHA256})
		expectedFiles = append(expectedFiles, file.Path)
	}
	matched := 0
	for _, service := range spec.Services {
		for _, item := range observed {
			if item.Identity.EngineID != spec.ExpectedEngineID {
				return resource.Record{}, problem.New("DOCKER_ENGINE_CHANGED", "")
			}
			if item.ComposeProject != spec.ProjectName || item.ComposeService != service {
				continue
			}
			matched++
			if !req.AdoptExisting {
				return resource.Record{}, problem.New("RESOURCE_EXISTING_REQUIRES_REVIEW", spec.ResourceID)
			}
			if !sameResourcePath(item.WorkingDir, spec.WorkingDir) ||
				!sameComposeFiles(item.ComposeFiles, expectedFiles) {
				return resource.Record{}, problem.New("RESOURCE_SOURCE_MISMATCH", spec.ResourceID)
			}
			if item.ConfigHash == "" {
				return resource.Record{}, problem.New("RESOURCE_CONFIG_UNVERIFIED", spec.ResourceID)
			}
			item.Identity.ControlID = grant
			record.Active = record.Active || item.Running
			record.Instances = append(record.Instances, item.Identity)
			record.ContainerConfigHashes[item.Identity.ContainerID] = item.ConfigHash
		}
	}
	if req.AdoptExisting && matched == 0 {
		return resource.Record{}, problem.New("RESOURCE_EXISTING_MISSING", spec.ResourceID)
	}
	return repo.SaveResource(ctx, record, req.ExpectedFingerprint)
}

// Acquire is called with a persisted run identity before a project consumes the resource.
func (s *ResourceService) Acquire(ctx context.Context, id, fingerprint string, use resource.Use) error {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo, err := s.readyRepository()
	if err != nil {
		return err
	}
	return repo.AcquireResource(ctx, id, fingerprint, use)
}

// Release runs only after this user's dependent units have stopped normally.
func (s *ResourceService) Release(ctx context.Context, id string, use resource.Use) error {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo, err := s.readyRepository()
	if err != nil {
		return err
	}
	return repo.ReleaseResource(ctx, id, use)
}

// StopUnused preserves references, other consumers, and shared resources during a
// single-project stop. On an incomplete stop it leaves downstream dependencies up.
func (s *ResourceService) StopUnused(ctx context.Context, id string, globalExit bool) (ResourceStopSummary, error) {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	result := ResourceStopSummary{Results: []ports.ContainerStopResult{}}
	repo, err := s.readyRepository()
	if err != nil {
		return result, err
	}
	record, err := repo.Resource(ctx, id)
	if err != nil {
		return result, err
	}
	switch {
	case record.Control != "managed":
		result.Reason = "reference"
		return result, nil
	case record.ControlGrant == "" || record.Fingerprint == "":
		result.Reason = "control-required"
		return result, nil
	case record.PendingStart != "":
		result.Reason = "start-unresolved"
		return result, nil
	case len(record.Users) > 0:
		result.Reason = "in-use"
		return result, nil
	case record.Shared && !globalExit:
		result.Reason = "shared-kept-running"
		return result, nil
	}
	for i := len(record.Instances) - 1; i >= 0; i-- {
		stopped, err := s.containers.Stop(ctx, record.Instances[i])
		if err != nil {
			return result, err
		}
		result.Results = append(result.Results, stopped)
		if !stopped.Graceful {
			result.Reason = "stop-incomplete"
			return result, nil
		}
	}
	if err := repo.MarkResourceStopped(ctx, id, record.Fingerprint); err != nil {
		return result, err
	}
	result.Stopped = true
	return result, nil
}

func sameResourcePath(first, second string) bool {
	return first != "" && second != "" &&
		strings.EqualFold(filepath.Clean(filepath.FromSlash(first)), filepath.Clean(filepath.FromSlash(second)))
}

func sameComposeFiles(label string, files []string) bool {
	parts := strings.Split(label, ",")
	if len(parts) != len(files) {
		return false
	}
	for i, part := range parts {
		if !sameResourcePath(part, files[i]) {
			return false
		}
	}
	return true
}
