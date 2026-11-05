package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func openDockerWorkspace(workspace string) (*os.Root, error) {
	root, err := filesystem.OpenLocalRoot(workspace)
	if err != nil {
		return nil, err
	}
	file, err := root.Open("workspace.json")
	if err != nil {
		root.Close()
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	file.Close()
	if err != nil {
		root.Close()
		return nil, err
	}
	if _, err := contract.DecodeWorkspace(data); err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}

func composeFileHash(name string) (string, error) {
	parent, err := filesystem.OpenLocalRoot(filepath.Dir(name))
	if err != nil {
		return "", err
	}
	defer parent.Close()
	info, err := parent.Lstat(filepath.Base(name))
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16*1024*1024 {
		return "", problem.New("COMPOSE_INPUT_INVALID", "")
	}
	file, err := parent.Open(filepath.Base(name))
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(file, 16*1024*1024+1))
	if err != nil || n > 16*1024*1024 {
		return "", problem.New("COMPOSE_INPUT_INVALID", "")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// PreviewCompose inspects only bounded config bytes and returns a hash, never raw
// resolved config/environment. Restore/start hooks need a separately reviewed step.
func (a *NativeContainerAdapter) PreviewCompose(
	ctx context.Context,
	spec ports.ComposeSpec,
) (ports.ComposePreview, error) {
	var result ports.ComposePreview
	if a.runner == nil {
		return result, problem.New("COMPOSE_RUNNER_MISSING", "")
	}
	root, err := openDockerWorkspace(a.workspace)
	if err != nil {
		return result, err
	}
	root.Close()
	if spec.ProjectName == "" {
		spec.ProjectName, err = ComposeProject(spec.ResourceID)
		if err != nil {
			return result, err
		}
	}
	spec.Files = slices.Clone(spec.Files)
	spec.Services = slices.Clone(spec.Services)
	for i, file := range spec.Files {
		digest, err := composeFileHash(file.Path)
		if err != nil {
			return result, err
		}
		if file.SHA256 != "" && file.SHA256 != digest {
			return result, problem.New("COMPOSE_INPUT_CHANGED", "")
		}
		spec.Files[i].SHA256 = digest
	}
	args, err := ComposeArguments(spec, "config")
	if err != nil {
		return result, err
	}
	data, err := a.runner.RunCompose(
		ctx,
		ports.ComposeInvocation{Pipe: a.engines[spec.Engine].pipe, Arguments: args, CWD: spec.WorkingDir},
	)
	if err != nil {
		return result, err
	}
	defer clear(data)
	if len(data) > 16*1024*1024 {
		return result, problem.New("COMPOSE_CONFIG_LIMIT", "")
	}
	var config map[string]any
	if json.Unmarshal(data, &config) != nil {
		return result, problem.New("COMPOSE_CONFIG_INVALID", "")
	}
	services, ok := config["services"].(map[string]any)
	if !ok {
		return result, problem.New("COMPOSE_CONFIG_INVALID", "")
	}
	ordered, err := composeOrder(services, spec.Services)
	if err != nil {
		return result, err
	}
	spec.Services = ordered
	canonical, err := json.Marshal(config)
	if err != nil {
		return result, err
	}
	defer clear(canonical)
	fixed := spec
	fixed.ControlGrant = ""
	fixed.ConfigFingerprint = ""
	payload, _ := json.Marshal(struct {
		Spec   ports.ComposeSpec
		Config json.RawMessage
	}{fixed, canonical})
	defer clear(payload)
	digest := sha256.Sum256(payload)
	spec.ConfigFingerprint = hex.EncodeToString(digest[:])
	return ports.ComposePreview{
		Spec:        spec,
		ProjectName: spec.ProjectName,
		Fingerprint: spec.ConfigFingerprint,
		Effects: []string{
			"create-or-start-selected-containers",
			"create-required-networks-and-volumes",
			"no-image-pull-or-build",
			"no-container-recreate",
		},
	}, nil
}

// composeOrder validates the explicitly selected closure. --no-deps prevents
// Compose from silently starting an unreviewed service outside this closure.
func composeOrder(config map[string]any, selected []string) ([]string, error) {
	dependencies := map[string][]string{}
	for _, name := range selected {
		service, ok := config[name].(map[string]any)
		if !ok {
			return nil, problem.New("COMPOSE_SERVICE_MISSING", name)
		}
		for _, hook := range []string{"pre_stop", "post_start"} {
			if value, ok := service[hook]; ok && value != nil {
				if items, ok := value.([]any); !ok || len(items) > 0 {
					return nil, problem.New("COMPOSE_HOOKS_REQUIRE_REVIEW", name)
				}
			}
		}
		switch value := service["depends_on"].(type) {
		case map[string]any:
			for dependency := range value {
				dependencies[name] = append(dependencies[name], dependency)
			}
			sort.Strings(dependencies[name])
		case []any:
			for _, dependency := range value {
				text, ok := dependency.(string)
				if !ok {
					return nil, problem.New("COMPOSE_CONFIG_INVALID", name)
				}
				dependencies[name] = append(dependencies[name], text)
			}
		case nil:
		default:
			return nil, problem.New("COMPOSE_CONFIG_INVALID", name)
		}
		for _, dependency := range dependencies[name] {
			if !slices.Contains(selected, dependency) {
				return nil, problem.New("COMPOSE_DEPENDENCY_NOT_SELECTED", dependency)
			}
		}
	}
	states := map[string]int{}
	ordered := []string{}
	var visit func(string) error
	visit = func(name string) error {
		if states[name] == 1 {
			return problem.New("COMPOSE_DEPENDENCY_CYCLE", name)
		}
		if states[name] == 2 {
			return nil
		}
		states[name] = 1
		for _, dependency := range dependencies[name] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		states[name] = 2
		ordered = append(ordered, name)
		return nil
	}
	for _, name := range selected {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}
