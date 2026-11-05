package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

const managerResourceLabel = "io.localprojecthub.resource-id"
const managerGrantLabel = "io.localprojecthub.control-grant"
const managerOperationLabel = "io.localprojecthub.operation-id"

// Start performs only a granted fixed-source Compose operation. Post-operation
// inventory is retained even on CLI failure so rollback cannot lose partial effects.
func (a *NativeContainerAdapter) Start(ctx context.Context, spec ports.ComposeSpec) ([]run.Identity, error) {
	authority, ok := a.authorization.(ports.ComposeAuthorization)
	if !ok {
		return nil, problem.New("RESOURCE_CONTROL_NOT_GRANTED", "")
	}
	preview, err := a.PreviewCompose(ctx, spec)
	if err != nil {
		return nil, err
	}
	if spec.ConfigFingerprint == "" || preview.Fingerprint != spec.ConfigFingerprint {
		return nil, problem.New("COMPOSE_INPUT_CHANGED", "")
	}
	spec = preview.Spec
	if err := authority.AuthorizeCompose(ctx, spec); err != nil {
		return nil, err
	}
	engineID, err := a.engineInfo(ctx, spec.Engine)
	if err != nil {
		return nil, err
	}
	if engineID != spec.ExpectedEngineID {
		return nil, problem.New("DOCKER_ENGINE_CHANGED", "")
	}
	before, err := a.Inventory(ctx, spec.Engine)
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	for _, item := range before {
		if item.ComposeProject != spec.ProjectName || !slices.Contains(spec.Services, item.ComposeService) {
			continue
		}
		item.Identity.ControlID = spec.ControlGrant
		if err := a.authorization.AuthorizeContainer(ctx, item.Identity); err != nil {
			return nil, problem.New("COMPOSE_EXISTING_INSTANCE_UNGRANTED", "")
		}
		existing[item.Identity.ContainerID] = true
	}
	attempt, err := identity.NewUUID()
	if err != nil {
		return nil, err
	}
	overlay, err := a.composeOverlay(spec, attempt)
	if err != nil {
		return nil, err
	}
	invocation := spec
	invocation.Files = append(slices.Clone(spec.Files), overlay)
	args, err := ComposeArguments(invocation, "up")
	if err != nil {
		return nil, err
	}
	if err := authority.BeginCompose(ctx, spec, attempt); err != nil {
		return nil, err
	}
	_, startErr := a.runner.RunCompose(
		ctx,
		ports.ComposeInvocation{Pipe: a.engines[spec.Engine].pipe, Arguments: args, CWD: spec.WorkingDir},
	)
	// A cancelled CLI may already have created containers. Observe those effects
	// under a separate bounded lifetime before returning the original failure.
	observeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	after, err := a.Inventory(observeCtx, spec.Engine)
	if err != nil {
		return nil, problem.New("COMPOSE_START_UNCERTAIN", "")
	}
	instances := []run.Identity{}
	for _, service := range spec.Services {
		for _, item := range after {
			if item.Identity.EngineID != spec.ExpectedEngineID {
				return instances, problem.New("DOCKER_ENGINE_CHANGED", "")
			}
			if item.ComposeProject != spec.ProjectName || item.ComposeService != service {
				continue
			}
			if !existing[item.Identity.ContainerID] &&
				(item.ManagerResourceID != spec.ResourceID || item.ManagerGrantID != spec.ControlGrant || item.ManagerOperationID != attempt) {
				return instances, problem.New("COMPOSE_START_UNCERTAIN", "")
			}
			item.Identity.ControlID = spec.ControlGrant
			instances = append(instances, item.Identity)
		}
	}
	if err := authority.RecordComposeInstances(observeCtx, spec, attempt, instances, startErr == nil); err != nil {
		return instances, err
	}
	if startErr != nil {
		return instances, startErr
	}
	for _, service := range spec.Services {
		if !slices.ContainsFunc(after, func(item ports.ContainerObservation) bool {
			return item.ComposeProject == spec.ProjectName && item.ComposeService == service && item.Running
		}) {
			return instances, problem.New("COMPOSE_SERVICE_NOT_RUNNING", service)
		}
	}
	return instances, nil
}

func (a *NativeContainerAdapter) composeOverlay(spec ports.ComposeSpec, attempt string) (ports.ComposeFile, error) {
	root, err := openDockerWorkspace(a.workspace)
	if err != nil {
		return ports.ComposeFile{}, err
	}
	defer root.Close()
	services := map[string]any{}
	for _, service := range spec.Services {
		services[service] = map[string]any{
			"labels": map[string]string{
				managerResourceLabel:  spec.ResourceID,
				managerGrantLabel:     spec.ControlGrant,
				managerOperationLabel: attempt,
			},
		}
	}
	data, err := json.MarshalIndent(map[string]any{"services": services}, "", "  ")
	if err != nil {
		return ports.ComposeFile{}, err
	}
	relative := path.Join("resources", spec.ResourceID, "compose-overrides", attempt+".json")
	if err := root.MkdirAll(path.Dir(relative), 0700); err != nil {
		return ports.ComposeFile{}, err
	}
	file, err := root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ports.ComposeFile{}, err
	}
	_, writeErr := file.Write(data)
	syncErr, closeErr := file.Sync(), file.Close()
	if writeErr != nil {
		return ports.ComposeFile{}, writeErr
	}
	if syncErr != nil {
		return ports.ComposeFile{}, syncErr
	}
	if closeErr != nil {
		return ports.ComposeFile{}, closeErr
	}
	digest := sha256.Sum256(data)
	return ports.ComposeFile{
		Path:   filepath.Join(a.workspace, filepath.FromSlash(relative)),
		SHA256: hex.EncodeToString(digest[:]),
	}, nil
}
