package docker

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// NativeComposeRunner executes a selected Docker binary with fixed argv and a
// disposable tool broker. Raw config/errors are never published as ordinary logs.
type NativeComposeRunner struct {
	executable string
	tools      ports.ToolRunner
}

// NewComposeRunner does not search or install tools implicitly; discovery supplies the executable.
func NewComposeRunner(executable string, tools ports.ToolRunner) (*NativeComposeRunner, error) {
	if !filepath.IsAbs(executable) || tools == nil {
		return nil, problem.New("COMPOSE_RUNNER_MISSING", "")
	}
	root, err := filesystem.OpenLocalRoot(filepath.Dir(executable))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(filepath.Base(executable))
	if err != nil || !info.Mode().IsRegular() {
		return nil, problem.New("COMPOSE_RUNNER_MISSING", "")
	}
	return &NativeComposeRunner{executable: executable, tools: tools}, nil
}

// RunCompose bounds output and strips inherited Docker context overrides/secrets.
func (r *NativeComposeRunner) RunCompose(ctx context.Context, call ports.ComposeInvocation) ([]byte, error) {
	if !localPipe.MatchString(call.Pipe) || !validComposeInvocation(call.Arguments) {
		return nil, problem.New("COMPOSE_ACTION_UNSUPPORTED", "")
	}
	if len(call.Input) > 16*1024*1024 || len(call.Input) > 0 && !slices.Contains(call.Arguments, "--hash") {
		return nil, problem.New("COMPOSE_INPUT_INVALID", "")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stdout := &composeOutput{limit: 16 * 1024 * 1024, cancel: cancel}
	stderr := &composeOutput{limit: 64 * 1024, cancel: cancel}
	host := "npipe://" + filepath.ToSlash(call.Pipe)
	args := append([]string{"--host", host}, call.Arguments...)
	env := map[string]string{}
	// Docker CLI locates system plugins through ProgramFiles on Windows.
	for _, name := range []string{"SYSTEMROOT", "WINDIR", "COMSPEC", "SYSTEMDRIVE", "PATH", "PATHEXT", "TEMP", "TMP", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "ProgramFiles"} {
		if value := os.Getenv(name); value != "" {
			env[name] = value
		}
	}
	env["COMPOSE_ANSI"] = "never"
	env["COMPOSE_DISABLE_ENV_FILE"] = "1"
	env["COMPOSE_PROGRESS"] = "plain"
	env["COMPOSE_PARALLEL_LIMIT"] = "4"
	err := r.tools.RunTool(
		ctx,
		ports.ProcessCommand{Executable: r.executable, Args: args, CWD: call.CWD, Environment: env, Input: call.Input},
		stdout,
		stderr,
	)
	defer clear(stderr.data)
	if stdout.exceeded || stderr.exceeded {
		clear(stdout.data)
		return nil, problem.New("COMPOSE_OUTPUT_LIMIT", "")
	}
	if err != nil {
		clear(stdout.data)
		return nil, problem.New("COMPOSE_EXECUTION_FAILED", "")
	}
	return stdout.data, nil
}

func validComposeInvocation(args []string) bool {
	if len(args) < 2 || args[0] != "compose" || len(args) > 1024 {
		return false
	}
	index, files, project, directory := 1, 0, false, false
	for index < len(args) {
		flag := args[index]
		if flag != "--project-name" && flag != "--project-directory" && flag != "--file" && flag != "--env-file" {
			break
		}
		if index+1 >= len(args) {
			return false
		}
		switch flag {
		case "--project-name":
			if project {
				return false
			}
			project = true
		case "--project-directory":
			if directory {
				return false
			}
			directory = true
		case "--file":
			files++
		}
		index += 2
	}
	if !project || !directory || files == 0 || index >= len(args) {
		return false
	}
	tail := args[index:]
	if tail[0] == "config" {
		if slices.Equal(tail, []string{"config", "--format", "json"}) ||
			slices.Equal(tail, []string{"config", "--hash", "*", "--no-interpolate"}) {
			return true
		}
		return slices.Equal(
			tail,
			[]string{"config", "--format", "json", "--no-interpolate", "--no-env-resolution", "--no-normalize"},
		)
	}
	prefix := []string{"up", "--detach", "--no-deps", "--no-build", "--no-recreate", "--pull", "never"}
	if len(tail) <= len(prefix) || !slices.Equal(tail[:len(prefix)], prefix) {
		return false
	}
	for _, service := range tail[len(prefix):] {
		if !composeService.MatchString(service) || strings.HasPrefix(service, "-") {
			return false
		}
	}
	return true
}

type composeOutput struct {
	mu       sync.Mutex
	data     []byte
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (w *composeOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.data)+len(data) > w.limit {
		w.exceeded = true
		w.cancel()
		return len(data), nil
	}
	w.data = append(w.data, data...)
	return len(data), nil
}
