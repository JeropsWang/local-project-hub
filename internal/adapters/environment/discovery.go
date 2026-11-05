// Package environment discovers tools without executing managed project code.
package environment

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// NativeProbe permits only catalog-defined version commands.
type NativeProbe struct{}

// NewProbe creates a stateless read-only native discovery adapter.
func NewProbe() *NativeProbe { return &NativeProbe{} }

// Discover enforces output and time budgets, and strips project-sensitive environment options.
func (p *NativeProbe) Discover(ctx context.Context, req ports.ProbeRequest) (ports.Observation, error) {
	obs := ports.Observation{
		Component: req.Tool,
		Status:    check.Unknown,
		Evidence:  check.Evidence{Kind: "observation", ObservedAt: time.Now().UTC()},
	}
	program, args, err := toolArgs(req.Tool)
	if err != nil {
		return obs, err
	}
	if len(req.Args) != 0 || req.CWD != "" {
		return obs, problem.New("PROBE_CUSTOM_COMMAND_REJECTED", "")
	}
	if req.Tool == "external" {
		obs.Status = check.BindingRequired
		obs.ReasonCode = "EXTERNAL_BINDING_REQUIRED"
		return obs, nil
	}
	if req.Location == "docker" && req.Tool != "docker" && req.Tool != "compose" {
		obs.ReasonCode = "CONTAINER_OBSERVATION_REQUIRED"
		return obs, nil
	}
	locator := req.Executable
	if locator == "" {
		locator, err = exec.LookPath(program)
		if err != nil {
			obs.Status = check.Missing
			obs.ReasonCode = "TOOL_MISSING"
			return obs, nil
		}
	} else if info, e := os.Stat(locator); e == nil && info.IsDir() {
		locator = filepath.Join(locator, program)
	}
	if !filepath.IsAbs(locator) {
		return obs, problem.New("PROBE_EXECUTABLE_ABSOLUTE_REQUIRED", "")
	}
	obs.Locator = locator
	obs.Evidence.Source = locator
	if req.Tool == "npm" {
		file := filepath.Join(filepath.Dir(locator), "node_modules", "npm", "package.json")
		data, err := readSmallRegularFile(file, 1024*1024)
		if err != nil {
			obs.Status = check.Missing
			obs.ReasonCode = "TOOL_MISSING"
			return obs, nil
		}
		var pkg struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &pkg) != nil || pkg.Version == "" {
			obs.ReasonCode = "TOOL_METADATA_INVALID"
			return obs, nil
		}
		obs.Version = pkg.Version
		obs.Evidence.Version = obs.Version
		obs.Evidence.Kind = "metadata"
		obs.Evidence.Source = file
		return obs, nil
	}
	timeout := 5 * time.Second
	if req.Timeout > 0 && req.Timeout < timeout {
		timeout = req.Timeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output := &boundedOutput{cancel: cancel}
	cmd := exec.CommandContext(probeCtx, locator, args...)
	cmd.Dir = filepath.Dir(locator)
	cmd.Env = probeEnvironment(req.Tool)
	hideProbeWindow(cmd)
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 200 * time.Millisecond
	err = cmd.Run()
	obs.OutputBytes = len(output.data)
	if output.exceeded {
		obs.ReasonCode = "PROBE_OUTPUT_LIMIT"
		return obs, nil
	}
	if probeCtx.Err() != nil {
		obs.ReasonCode = "PROBE_TIMEOUT"
		return obs, nil
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			obs.ExitCode = exit.ExitCode()
		}
		obs.ReasonCode = "PROBE_FAILED"
		return obs, nil
	}
	obs.Version, obs.Architecture, obs.ABI, obs.Capabilities = parseVersion(req.Tool, string(output.data), locator)
	if obs.Version == "" {
		obs.ReasonCode = "PROBE_VERSION_UNRECOGNIZED"
		return obs, nil
	}
	obs.Evidence.Version = obs.Version
	return obs, nil
}

type boundedOutput struct {
	mu       sync.Mutex
	data     []byte
	exceeded bool
	cancel   context.CancelFunc
}

func (w *boundedOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := 64*1024 - len(w.data)
	if len(data) > remaining {
		w.data = append(w.data, data[:remaining]...)
		w.exceeded = true
		w.cancel()
	} else {
		w.data = append(w.data, data...)
	}
	return len(data), nil
}

func probeEnvironment(tool string) []string {
	result := []string{"GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "PYTHONDONTWRITEBYTECODE=1"}
	for _, key := range []string{"SystemRoot", "WINDIR", "PATH", "PATHEXT", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(key); ok {
			result = append(result, key+"="+value)
		}
	}
	if tool == "docker" || tool == "compose" {
		// Docker's Windows plugin discovery uses ProgramFiles, and its current
		// context uses the user's config home. Do not inherit arbitrary options.
		for _, key := range []string{"ProgramFiles", "USERPROFILE", "APPDATA", "LOCALAPPDATA"} {
			if value, ok := os.LookupEnv(key); ok {
				result = append(result, key+"="+value)
			}
		}
	}
	return result
}
