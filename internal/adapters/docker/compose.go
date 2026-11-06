package docker

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

var resourceUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var composeKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var composeService = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var composeProject = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// ComposeProject uses the entire UUID, independent of the source folder's name.
func ComposeProject(resourceID string) (string, error) {
	return resource.ComposeProject(resourceID)
}

// ComposeArguments preserves ordered inputs and declares one supported action.
// This pure preview builder does not authorize or execute its resulting command.
func ComposeArguments(spec ports.ComposeSpec, action string) ([]string, error) {
	project, err := ComposeProject(spec.ResourceID)
	if err != nil {
		return nil, err
	}
	if spec.ProjectName != "" && spec.ProjectName != project {
		if spec.ControlGrant == "" && action != "config" {
			return nil, problem.New("RESOURCE_CONTROL_NOT_GRANTED", "")
		}
		if !composeProject.MatchString(spec.ProjectName) {
			return nil, problem.New("COMPOSE_PROJECT_INVALID", "")
		}
		project = spec.ProjectName
	}
	if !filepath.IsAbs(spec.WorkingDir) || len(spec.Files) == 0 || len(spec.Files) > 32 || len(spec.Services) == 0 ||
		len(spec.Services) > 128 {
		return nil, problem.New("COMPOSE_SPEC_INVALID", "")
	}
	args := []string{"compose", "--project-name", project, "--project-directory", spec.WorkingDir}
	if spec.EnvironmentFile != nil {
		args = append(args, "--env-file", spec.EnvironmentFile.Path)
	}
	for _, file := range spec.Files {
		if !filepath.IsAbs(file.Path) || strings.HasPrefix(file.Path, `\\`) || strings.ContainsRune(file.Path, 0) {
			return nil, problem.New("COMPOSE_FILE_INVALID", "")
		}
		args = append(args, "--file", file.Path)
	}
	services := []string{}
	for _, service := range spec.Services {
		if !composeService.MatchString(service) || slices.Contains(services, service) {
			return nil, problem.New("COMPOSE_SERVICE_INVALID", "")
		}
		services = append(services, service)
	}
	switch action {
	case "config":
		args = append(args, "config", "--format", "json", "--no-interpolate", "--no-env-resolution", "--no-normalize")
	case "resolved-config":
		args = append(args, "config", "--format", "json")
	case "up":
		args = append(args, "up", "--detach", "--no-deps", "--no-build", "--no-recreate", "--pull", "never")
		args = append(args, services...)
	default:
		return nil, problem.New("COMPOSE_ACTION_UNSUPPORTED", "")
	}
	return args, nil
}
