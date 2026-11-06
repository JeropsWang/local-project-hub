package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// Compute service hashes using this exact Compose implementation. Resolve env
// files first, then feed the normalized model through stdin with interpolation
// disabled: older config --hash implementations skipped env_file resolution.
// Plain environment values never enter a returned preview, file or ordinary log.
func (a *NativeContainerAdapter) resolvedComposeHashes(ctx context.Context, spec *ports.ComposeSpec) error {
	args, err := ComposeArguments(*spec, "resolved-config")
	if err != nil {
		return err
	}
	data, err := a.runner.RunCompose(
		ctx,
		ports.ComposeInvocation{Pipe: a.engines[spec.Engine].pipe, Arguments: args, CWD: spec.WorkingDir},
	)
	if err != nil {
		return err
	}
	defer clear(data)
	if len(data) > 16*1024*1024 {
		return problem.New("COMPOSE_CONFIG_LIMIT", "")
	}
	var model map[string]any
	if json.Unmarshal(data, &model) != nil {
		return problem.New("COMPOSE_CONFIG_INVALID", "")
	}
	services, ok := model["services"].(map[string]any)
	if !ok {
		return problem.New("COMPOSE_CONFIG_INVALID", "")
	}
	for _, value := range services {
		if service, ok := value.(map[string]any); ok {
			delete(service, "env_file")
		}
	}
	normalized, err := json.Marshal(model)
	if err != nil {
		return err
	}
	defer clear(normalized)
	digest := sha256.Sum256(normalized)
	spec.ResolvedFingerprint = hex.EncodeToString(digest[:])
	hashArgs := []string{
		"compose",
		"--project-name",
		spec.ProjectName,
		"--project-directory",
		spec.WorkingDir,
		"--file",
		"-",
		"config",
		"--hash",
		"*",
		"--no-interpolate",
	}
	output, err := a.runner.RunCompose(
		ctx,
		ports.ComposeInvocation{
			Pipe:      a.engines[spec.Engine].pipe,
			Arguments: hashArgs,
			CWD:       spec.WorkingDir,
			Input:     normalized,
		},
	)
	if err != nil {
		return err
	}
	defer clear(output)
	if len(output) > 64*1024 {
		return problem.New("COMPOSE_HASH_INVALID", "")
	}
	hashes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !composeService.MatchString(fields[0]) || len(fields[1]) != 64 ||
			hashes[fields[0]] != "" {
			return problem.New("COMPOSE_HASH_INVALID", "")
		}
		decoded, err := hex.DecodeString(fields[1])
		if err != nil || len(decoded) != 32 || strings.ToLower(fields[1]) != fields[1] {
			return problem.New("COMPOSE_HASH_INVALID", "")
		}
		hashes[fields[0]] = fields[1]
	}
	for _, name := range spec.Services {
		if hashes[name] == "" {
			return problem.New("COMPOSE_HASH_INVALID", name)
		}
	}
	spec.ServiceConfigHashes = hashes
	return nil
}
