// Package process contains Windows lifecycle adapters; disposable preparation is
// deliberately separate from graceful shutdown of long-running business projects.
package process

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/preparation"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// NativePreparationRunner owns only short-lived install/build processes in new environments.
// A Windows Job prevents subprocesses from escaping cancellation. It is not an OS sandbox:
// package lifecycle scripts still execute with the user's filesystem permissions.
type NativePreparationRunner struct{ workspace string }

// NewPreparationRunner scopes commands to preparing receipts below this workspace's envs directory.
func NewPreparationRunner(workspace string) *NativePreparationRunner {
	return &NativePreparationRunner{workspace: filepath.Clean(workspace)}
}

// Run uses argv directly, private cache/home directories, bounded logs and no terminal window.
func (r *NativePreparationRunner) Run(
	ctx context.Context,
	request ports.PreparationCommand,
) (ports.PreparationResult, error) {
	result := ports.PreparationResult{ExitCode: -1}
	if !contained(filepath.Join(r.workspace, "envs"), request.OwnedRoot) ||
		!contained(request.OwnedRoot, request.CWD) ||
		!filepath.IsAbs(request.Executable) {
		return result, problem.New("PREPARATION_DIRECTORY_UNOWNED", "")
	}
	anchor, err := filesystem.OpenLocalRoot(request.OwnedRoot)
	if err != nil {
		return result, err
	}
	defer anchor.Close()
	// The marker is not a trust grant: only backend-authored operations reach this
	// port. It prevents accidentally directing installs into a ready/original tree.
	marker, err := anchor.Open(".lph-environment.json")
	if err != nil {
		return result, problem.New("PREPARATION_DIRECTORY_UNOWNED", "")
	}
	data, readErr := io.ReadAll(io.LimitReader(marker, 1024*1024+1))
	marker.Close()
	var receipt preparation.Receipt
	if readErr != nil || len(data) > 1024*1024 || json.Unmarshal(data, &receipt) != nil ||
		receipt.State != "preparing" ||
		!strings.EqualFold(filepath.Clean(receipt.Root), filepath.Clean(request.OwnedRoot)) {
		return result, problem.New("PREPARATION_DIRECTORY_UNOWNED", "")
	}
	cwdAnchor, err := filesystem.OpenLocalRoot(request.CWD)
	if err != nil {
		return result, err
	}
	cwdAnchor.Close()
	for _, dir := range []string{"logs", "home", "home/AppData/Roaming", "home/AppData/Local", "temp", "cache"} {
		if err := anchor.MkdirAll(dir, 0700); err != nil {
			return result, err
		}
	}
	nonce, err := identity.NewUUID()
	if err != nil {
		return result, err
	}
	logName := "logs/prepare-" + nonce + ".log"
	logFile, err := anchor.OpenFile(logName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, err
	}
	defer logFile.Close()
	result.LogFile = filepath.Join(request.OwnedRoot, filepath.FromSlash(logName))
	output := &limitedPreparationLog{file: logFile}
	timeout := request.Timeout
	if timeout <= 0 || timeout > 30*time.Minute {
		timeout = 20 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := runCtx.Err(); err != nil {
		return result, err
	}
	job, err := newDisposableJob()
	if err != nil {
		return result, err
	}
	defer windows.CloseHandle(job)
	cmd := exec.Command(request.Executable, request.Args...)
	cmd.Dir, cmd.Env = request.CWD, privatePreparationEnvironment(request)
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = output, output, 2*time.Second
	// Assign before resuming, so even a very fast lifecycle script cannot spawn
	// a child between CreateProcess and assignment to the owned Job.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW,
	}
	if err := cmd.Start(); err != nil {
		return result, err
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(cmd.Process.Pid),
	)
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
	}
	if err == nil {
		err = resumeSuspendedThread(uint32(cmd.Process.Pid))
	}
	if err != nil {
		// It is still the exact process handle we just created, never a discovered PID.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return result, problem.New("PREPARATION_JOB_ASSIGNMENT_FAILED", "")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-runCtx.Done():
		_ = windows.TerminateJobObject(job, 1)
		<-done
		err = runCtx.Err()
	}
	// Successful root exit must not leave detached package-manager children.
	_ = windows.TerminateJobObject(job, 0)
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if syncErr := logFile.Sync(); err == nil && syncErr != nil {
		err = syncErr
	}
	if output.err != nil && err == nil {
		err = output.err
	}
	if err != nil {
		if runCtx.Err() != nil {
			return result, runCtx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return result, problem.New("DEPENDENCY_RESTORE_FAILED", "")
		}
		return result, problem.New("PREPARATION_PROCESS_FAILED", "")
	}
	return result, nil
}

func privatePreparationEnvironment(request ports.PreparationCommand) []string {
	values := map[string]string{}
	for _, key := range []string{"SYSTEMROOT", "WINDIR", "COMSPEC", "SYSTEMDRIVE", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE"} {
		if value := os.Getenv(key); value != "" {
			values[key] = value
		}
	}
	for key, value := range request.Environment {
		values[strings.ToUpper(key)] = value
	}
	root := request.OwnedRoot
	values["TEMP"], values["TMP"] = filepath.Join(root, "temp"), filepath.Join(root, "temp")
	values["HOME"], values["USERPROFILE"] = filepath.Join(root, "home"), filepath.Join(root, "home")
	values["APPDATA"], values["LOCALAPPDATA"] = filepath.Join(
		root,
		"home",
		"AppData",
		"Roaming",
	), filepath.Join(
		root,
		"home",
		"AppData",
		"Local",
	)
	values["PATH"] += string(os.PathListSeparator) + filepath.Join(os.Getenv("SYSTEMROOT"), "System32")
	keys := []string{}
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := []string{}
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func contained(root, candidate string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(candidate) {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}

type limitedPreparationLog struct {
	mu   sync.Mutex
	file *os.File
	size int64
	err  error
}

func (w *limitedPreparationLog) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := int64(1024*1024) - w.size
	if remaining > 0 && w.err == nil {
		part := data
		if int64(len(part)) > remaining {
			part = part[:remaining]
		}
		n, err := w.file.Write(part)
		w.size += int64(n)
		w.err = err
	}
	// Continue draining after the cap to avoid deadlocking child stdout/stderr.
	return len(data), nil
}
