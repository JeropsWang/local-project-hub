package ports

import (
	"context"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

// ProcessCommand is backend-resolved argv. Environment values are transient and
// must not be persisted in the portable document or logged as command metadata.
type ProcessCommand struct {
	Executable  string
	Args        []string
	CWD         string
	Environment map[string]string
}

// ProcessScript carries reviewed immutable bytes; ${file:id} arguments resolve to
// the manager's run snapshot rather than an editable original script.
type ProcessScript struct {
	Name   string
	Bytes  []byte
	SHA256 string
}

// ProcessSpec fixes launch and normal-stop behavior for this process's whole lifetime.
type ProcessSpec struct {
	RunID        string
	UnitID       string
	Command      ProcessCommand
	Stop         StopSpec
	Scripts      map[string]ProcessScript
	SecretValues []string
}

// StopSpec's command/kind are captured at Start. Later calls supply only operation
// identity and wait policy; edited commands cannot replace a running instance's stop.
type StopSpec struct {
	Kind        string
	OperationID string
	Command     *ProcessCommand
	Timeout     time.Duration
	WaitOnly    bool
}

// InstanceState distinguishes root exit from exit of all owned descendants.
type InstanceState struct {
	RootRunning            bool
	ActiveProcesses        uint32
	Exited                 bool
	ExitCode               uint32
	ExitCodesVerified      bool
	ExitFailure            bool
	ExitVerificationFailed bool
}

// StopResult requires both a normal completion witness and actual process-tree exit.
type StopResult struct {
	Graceful        bool
	Accepted        bool
	RootExited      bool
	ActiveProcesses uint32
	Code            string
}

// ProcessAdapter never implicitly terminates a running business process on timeout.
type ProcessAdapter interface {
	Start(context.Context, ProcessSpec) (run.Identity, error)
	Stop(context.Context, run.Identity, StopSpec) (StopResult, error)
	Inspect(context.Context, run.Identity) (InstanceState, error)
}
