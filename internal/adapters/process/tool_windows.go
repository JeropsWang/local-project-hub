package process

import (
	"context"
	"io"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type nativeToolRunner struct{}

// NewToolRunner contains CLI/plugin subprocesses in a disposable Job. Business
// projects use NativeProcessAdapter's separate non-killing Job policy.
func NewToolRunner() ports.ToolRunner { return nativeToolRunner{} }

func (nativeToolRunner) RunTool(ctx context.Context, spec ports.ProcessCommand, stdout, stderr io.Writer) error {
	if err := validateProcessCommand(spec); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		ctx = bounded
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	job, err := newDisposableJob()
	if err != nil {
		return err
	}
	defer windows.CloseHandle(job)
	command := exec.Command(spec.Executable, spec.Args...)
	command.Dir = spec.CWD
	command.Env = []string{}
	for name, value := range spec.Environment {
		command.Env = append(command.Env, name+"="+value)
	}
	command.Stdout, command.Stderr = stdout, stderr
	command.WaitDelay = 2 * time.Second
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
	if err == nil {
		// Root exit can precede plugin/console-host exit. Wait for the owned
		// controller group to converge before calling an external effect complete.
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			active, _, queryErr := businessJobProcesses(job)
			if queryErr != nil {
				return queryErr
			}
			if active == 0 {
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
