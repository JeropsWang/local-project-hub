package archive

import (
	"bytes"
	"io"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

// validateComposeClosure supports literal, colocated configuration references.
// Source trees, bind mounts and dynamic paths require an external Compose file
// binding. The exporter never rewrites YAML or executes Compose to infer inputs.
func validateComposeClosure(doc project.Document, files map[string][]byte) error {
	state := map[string]int{}
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return problem.New("BUNDLE_COMPOSE_CYCLE", name)
		case 2:
			return nil
		}
		data, ok := files[name]
		if !ok {
			return problem.New("BUNDLE_COMPOSE_REFERENCE_MISSING", name)
		}
		state[name] = 1
		var model map[string]any
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		if err := decoder.Decode(&model); err != nil || model == nil {
			return problem.New("BUNDLE_COMPOSE_INVALID", name)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return problem.New("BUNDLE_COMPOSE_INVALID", name)
		}
		resolve := func(raw any, nested bool) error {
			ref, ok := raw.(string)
			ref = strings.TrimPrefix(ref, "./")
			if !ok || ref == "." || strings.Contains(ref, "$") || project.ValidateRelativePath(ref) != nil {
				return problem.New("BUNDLE_COMPOSE_REFERENCE_UNSUPPORTED", name)
			}
			target := path.Join(path.Dir(name), ref)
			if nested {
				return visit(target)
			}
			if _, ok := files[target]; !ok {
				return problem.New("BUNDLE_COMPOSE_REFERENCE_MISSING", target)
			}
			return nil
		}
		if value, exists := model["include"]; exists {
			items, ok := value.([]any)
			if !ok {
				return problem.New("BUNDLE_COMPOSE_REFERENCE_UNSUPPORTED", name)
			}
			for _, item := range items {
				if err := resolve(item, true); err != nil {
					return err
				}
			}
		}
		volumes, _ := model["volumes"].(map[string]any)
		for _, value := range volumes {
			if config, ok := value.(map[string]any); ok && config["driver_opts"] != nil {
				return problem.New("BUNDLE_COMPOSE_BIND_EXTERNAL_REQUIRED", name)
			}
		}
		for _, kind := range []string{"configs", "secrets"} {
			items, _ := model[kind].(map[string]any)
			for _, item := range items {
				config, _ := item.(map[string]any)
				if value, ok := config["file"]; ok {
					if kind == "secrets" {
						return problem.New("BUNDLE_COMPOSE_SECRET_EXTERNAL_REQUIRED", name)
					}
					if err := resolve(value, false); err != nil {
						return err
					}
				}
			}
		}
		services, _ := model["services"].(map[string]any)
		for _, item := range services {
			service, ok := item.(map[string]any)
			if !ok {
				return problem.New("BUNDLE_COMPOSE_INVALID", name)
			}
			for _, key := range []string{"build", "label_file", "develop", "provider", "credential_spec", "devices"} {
				if _, exists := service[key]; exists {
					return problem.New("BUNDLE_COMPOSE_REFERENCE_UNSUPPORTED", name)
				}
			}
			if ext, ok := service["extends"].(map[string]any); ok {
				if value, exists := ext["file"]; exists {
					if err := resolve(value, true); err != nil {
						return err
					}
				}
			}
			if value, exists := service["env_file"]; exists {
				entries, ok := value.([]any)
				if !ok {
					entries = []any{value}
				}
				for _, entry := range entries {
					if detail, ok := entry.(map[string]any); ok {
						entry = detail["path"]
					}
					envPath, _ := entry.(string)
					if !strings.HasSuffix(envPath, ".example") && !strings.HasSuffix(envPath, ".template") {
						return problem.New("BUNDLE_COMPOSE_SECRET_EXTERNAL_REQUIRED", name)
					}
					if err := resolve(entry, false); err != nil {
						return err
					}
				}
			}
			if volumes, ok := service["volumes"].([]any); ok {
				for _, mount := range volumes {
					switch entry := mount.(type) {
					case string:
						source, target, hasSource := strings.Cut(entry, ":")
						if strings.Contains(entry, "$") || hasSource && (strings.ContainsAny(source, "/\\") || strings.HasPrefix(source, ".") || !strings.HasPrefix(target, "/")) {
							return problem.New("BUNDLE_COMPOSE_BIND_EXTERNAL_REQUIRED", name)
						}
					case map[string]any:
						if entry["type"] != "volume" && entry["type"] != "tmpfs" {
							return problem.New("BUNDLE_COMPOSE_BIND_EXTERNAL_REQUIRED", name)
						}
					default:
						return problem.New("BUNDLE_COMPOSE_INVALID", name)
					}
				}
			}
		}
		state[name] = 2
		return nil
	}
	for _, unit := range doc.Units {
		for _, id := range unit.ComposeFileIDs {
			file := doc.Files[id]
			if file.Availability == "included" {
				if err := visit(file.BundlePath); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
