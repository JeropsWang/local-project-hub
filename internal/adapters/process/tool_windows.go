package process

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type nativeToolRunner struct{ waitSafe bool }

// NewToolRunner contains CLI/plugin subprocesses in a disposable Job. Business
// projects use NativeProcessAdapter's separate non-killing Job policy.
func NewToolRunner() ports.ToolRunner { return nativeToolRunner{} }

func (r nativeToolRunner) RunTool(ctx context.Context, spec ports.ProcessCommand, stdout, stderr io.Writer) error {
	if err := validateProcessCommand(spec); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok && !r.waitSafe {
		bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		ctx = bounded
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var job windows.Handle
	var tracker *jobTracker
	var err error
	if r.waitSafe {
		// Saving/migration work is not a disposable CLI. Caller cancellation and
		// timeout cannot turn into a Job kill while this effect is in progress.
		ctx = context.WithoutCancel(ctx)
		job, tracker, err = newBusinessJob()
	} else {
		job, err = newDisposableJob()
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(job)
	if tracker != nil {
		defer tracker.close()
	}
	command := exec.Command(spec.Executable, spec.Args...)
	command.Dir = spec.CWD
	if len(spec.Input) > 0 {
		command.Stdin = bytes.NewReader(spec.Input)
	}
	command.Env = []string{}
	for name, value := range spec.Environment {
		command.Env = append(command.Env, name+"="+value)
	}
	command.Stdout, command.Stderr = stdout, stderr
	command.WaitDelay = 2 * time.Second
	if r.waitSafe {
		command.WaitDelay = 0
	}
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW,
	}
	if err := command.Start(); err != nil {
		return err
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(command.Process.Pid),
	)
	if err == nil {
		err = windows.AssignProcessToJobObject(job, handle)
		windows.CloseHandle(handle)
	}
	if err == nil {
		err = resumeSuspendedThread(uint32(command.Process.Pid))
	}
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return problem.New("TOOL_JOB_ASSIGNMENT_FAILED", "")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = windows.TerminateJobObject(job, 1)
		<-done
		return ctx.Err()
	}
	if err == nil || r.waitSafe {
		// Root exit can precede plugin/console-host exit. Wait for the owned
		// controller group to converge before calling an external effect complete.
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			active, total, queryErr := businessJobProcesses(job)
			if queryErr != nil {
				if r.waitSafe {
					return problem.New("UNIT_STATE_UNCERTAIN", "")
				}
				return queryErr
			}
			if active == 0 {
				if r.waitSafe {
					deadline := time.Now().Add(time.Second)
					for {
						verified, failed, unknown := tracker.exitStatus(total)
						if failed {
							return problem.New("UNIT_EXIT_FAILED", "")
						}
						if verified {
							return err
						}
						if unknown || time.Now().After(deadline) {
							return problem.New("UNIT_EXIT_UNVERIFIED", "")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
	return err
}
