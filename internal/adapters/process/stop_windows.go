package process

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/control"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// Stop initiates at most one normal operation and observes the whole Job. A wait
// timeout ends only this caller's wait; an accepted save continues independently.
func (a *NativeProcessAdapter) Stop(ctx context.Context, identity run.Identity, spec ports.StopSpec) (ports.StopResult, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var result ports.StopResult
	instance, err := a.original(identity)
	if err != nil {
		return result, err
	}
	if !control.ValidIdentity(spec.OperationID) {
		return result, problem.New("STOP_OPERATION_INVALID", "")
	}
	initial, err := inspectNative(instance)
	if err != nil {
		return result, err
	}
	instance.mu.Lock()
	if instance.operationDone == nil {
		if !initial.RootRunning || spec.WaitOnly {
			instance.mu.Unlock()
			return stopObservation(initial, ControlOutcome{}, "STOP_NOT_INITIATED"), nil
		}
		a.beginStop(instance, spec.OperationID)
	} else if instance.operationID != spec.OperationID {
		select {
		case <-instance.operationDone:
			if spec.WaitOnly || instance.controlResult.Completed {
				instance.mu.Unlock()
				return result, problem.New("STOP_OPERATION_CONFLICT", "")
			}
			a.beginStop(instance, spec.OperationID)
		default:
			instance.mu.Unlock()
			return result, problem.New("STOP_OPERATION_IN_PROGRESS", "")
		}
	}
	instance.mu.Unlock()
	timeout := spec.Timeout
	if timeout <= 0 || timeout > 10*time.Minute {
		timeout = 15 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := inspectNative(instance)
		if err != nil {
			return result, err
		}
		instance.mu.Lock()
		outcome, operationErr, done := instance.controlResult, instance.controlError, instance.operationDone
		operationFinished := false
		select {
		case <-done:
			operationFinished = true
		default:
		}
		instance.mu.Unlock()
		code := "STOP_WAIT_INCOMPLETE"
		if outcome.ErrorCode != "" {
			code = outcome.ErrorCode
		} else if operationErr != nil {
			code = "CONTROL_OPERATION_INCOMPLETE"
			var known *problem.Error
			if errors.As(operationErr, &known) {
				code = known.Code
			}
		}
		result = stopObservation(state, outcome, code)
		if state.Exited && operationFinished && (state.ExitCodesVerified || state.ExitFailure || state.ExitVerificationFailed) {
			if state.ExitFailure {
				result.Code = "PROCESS_EXIT_FAILED"
			} else if state.ExitVerificationFailed {
				result.Code = "PROCESS_EXIT_UNVERIFIED"
			} else if outcome.Completed && state.ExitCode == 0 {
				result.Graceful = true
				result.Code = ""
			} else {
				result.Code = "EXIT_WITHOUT_GRACEFUL_CONFIRMATION"
			}
			<-instance.waitDone
			_ = instance.log.Close()
			return result, nil
		}
		if operationFinished && !outcome.Completed {
			return result, nil
		}
		select {
		case <-waitCtx.Done():
			return result, nil
		case <-ticker.C:
		}
	}
}

// Caller holds instance.mu. A retry with a new operation ID is allowed only after
// the preceding call converged; continue-wait keeps the original request alive.
func (a *NativeProcessAdapter) beginStop(instance *nativeInstance, operationID string) {
	instance.operationID = operationID
	instance.operationDone = make(chan struct{})
	instance.controlResult = ControlOutcome{}
	instance.controlError = nil
	done := instance.operationDone
	if instance.stop.Kind == "managed-pipe" {
		operationCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		instance.controlCancel = cancel
		go func() {
			defer cancel()
			client := NewControlClient()
			client.observe = func(outcome ControlOutcome) {
				instance.mu.Lock()
				instance.controlResult = outcome
				instance.mu.Unlock()
			}
			outcome, err := client.Exchange(operationCtx, instance.identity, instance.token, operationID, "shutdown")
			instance.mu.Lock()
			instance.controlResult = outcome
			instance.controlError = err
			close(done)
			instance.mu.Unlock()
		}()
		return
	}
	go func() {
		err := a.executeStopCommand(instance)
		instance.mu.Lock()
		instance.controlResult = ControlOutcome{Accepted: err == nil, Completed: err == nil, State: "stopped"}
		instance.controlError = err
		close(done)
		instance.mu.Unlock()
	}()
}

func stopObservation(state ports.InstanceState, outcome ControlOutcome, code string) ports.StopResult {
	return ports.StopResult{Accepted: outcome.Accepted, RootExited: !state.RootRunning, ActiveProcesses: state.ActiveProcesses, Code: code}
}

func (a *NativeProcessAdapter) executeStopCommand(instance *nativeInstance) error {
	if instance.stop.Command == nil {
		return problem.New("STOP_COMMAND_MISSING", "")
	}
	for name, expected := range instance.scripts {
		root, err := filesystem.OpenLocalRoot(filepath.Dir(name))
		if err != nil {
			return problem.New("RUN_SCRIPT_CHANGED", "")
		}
		info, err := root.Lstat(filepath.Base(name))
		if err != nil || !info.Mode().IsRegular() || info.Size() > 16*1024*1024 {
			root.Close()
			return problem.New("RUN_SCRIPT_CHANGED", "")
		}
		file, err := root.Open(filepath.Base(name))
		root.Close()
		if err != nil {
			return problem.New("RUN_SCRIPT_CHANGED", "")
		}
		digest := sha256.New()
		n, readErr := io.Copy(digest, io.LimitReader(file, 16*1024*1024+1))
		file.Close()
		if readErr != nil || n > 16*1024*1024 || hex.EncodeToString(digest.Sum(nil)) != expected {
			return problem.New("RUN_SCRIPT_CHANGED", "")
		}
	}
	spec := *instance.stop.Command
	command := exec.Command(spec.Executable, spec.Args...)
	command.Dir = spec.CWD
	command.Env = businessEnvironment(spec.Environment)
	stdout, stderr := instance.log.Writer("stdout"), instance.log.Writer("stderr")
	defer stdout.Close()
	defer stderr.Close()
	command.Stdout, command.Stderr = stdout, stderr
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW}
	if err := command.Start(); err != nil {
		return problem.New("STOP_COMMAND_FAILED", "")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(instance.job, handle)
		windows.CloseHandle(handle)
	}
	if err == nil {
		err = resumeSuspendedThread(uint32(command.Process.Pid))
	}
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return problem.New("STOP_COMMAND_LAUNCH_FAILED", "")
	}
	if err := command.Wait(); err != nil {
		return problem.New("STOP_COMMAND_FAILED", "")
	}
	return nil
}
