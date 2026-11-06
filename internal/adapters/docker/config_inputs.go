package docker

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// Select the sole interpolation file explicitly. Native CLI disables implicit
// dotenv discovery, so COMPOSE_ENV_FILES cannot introduce unreviewed inputs.
func prepareComposeEnvironment(spec *ports.ComposeSpec) error {
	if !filepath.IsAbs(spec.WorkingDir) {
		return problem.New("COMPOSE_SPEC_INVALID", "")
	}
	spec.InputHashes = map[string]string{}
	spec.EnvironmentFile = nil
	name := filepath.Join(spec.WorkingDir, ".env")
	if _, err := os.Lstat(name); os.IsNotExist(err) {
		spec.InputHashes[name] = "missing"
		return nil
	} else if err != nil {
		return err
	}
	hash, err := composeFileHash(name)
	if err != nil {
		return err
	}
	spec.EnvironmentFile = &ports.ComposeFile{Path: name, SHA256: hash}
	spec.InputHashes[name] = hash
	return nil
}

// Hash referenced file bytes without returning plaintext. Dynamic paths are
// blocked until they can be resolved and pinned with the actual launch model.
func collectComposeInputFiles(spec *ports.ComposeSpec, config map[string]any) error {
	add := func(name string, required bool) error {
		if name == "" || strings.ContainsAny(name, "$\x00") || strings.HasPrefix(name, `\\`) {
			return problem.New("COMPOSE_INPUT_UNVERIFIED", "")
		}
		if !filepath.IsAbs(name) {
			name = filepath.Join(spec.WorkingDir, filepath.FromSlash(name))
		}
		if len(spec.InputHashes) >= 256 {
			return problem.New("COMPOSE_INPUT_LIMIT", "")
		}
		if _, err := os.Lstat(name); !required && os.IsNotExist(err) {
			spec.InputHashes[name] = "missing"
			return nil
		}
		hash, err := composeFileHash(name)
		if err != nil {
			return err
		}
		spec.InputHashes[name] = hash
		return nil
	}
	services, _ := config["services"].(map[string]any)
	for _, name := range spec.Services {
		service, _ := services[name].(map[string]any)
		value := service["env_file"]
		var files []any
		switch item := value.(type) {
		case nil:
		case string:
			files = []any{item}
		case []any:
			files = item
		default:
			return problem.New("COMPOSE_INPUT_UNVERIFIED", name)
		}
		for _, file := range files {
			path, required := "", true
			switch item := file.(type) {
			case string:
				path = item
			case map[string]any:
				path, _ = item["path"].(string)
				if value, ok := item["required"].(bool); ok {
					required = value
				}
			default:
				return problem.New("COMPOSE_INPUT_UNVERIFIED", name)
			}
			if err := add(path, required); err != nil {
				return err
			}
		}
	}
	for _, kind := range []string{"configs", "secrets"} {
		items, _ := config[kind].(map[string]any)
		for _, value := range items {
			item, _ := value.(map[string]any)
			if file, ok := item["file"].(string); ok {
				if err := add(file, true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
