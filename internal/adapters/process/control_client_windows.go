package process

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"time"
	"unsafe"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/JeropsWang/local-project-hub/internal/domain/control"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

// ControlOutcome is protocol evidence only. The caller must also verify process-tree exit.
type ControlOutcome struct {
	Accepted  bool
	Completed bool
	State     string
	ErrorCode string
}

// ControlClient validates framed responses and never places tokens in an error or log.
type ControlClient struct {
	dial    func(context.Context, run.Identity) (net.Conn, error)
	observe func(ControlOutcome)
}

// NewControlClient connects only to the local per-run pipe of the original process.
func NewControlClient() *ControlClient { return &ControlClient{dial: dialControlPipe} }

// Exchange sends one operation and reads until terminal evidence or the explicit wait deadline.
// Disconnect/timeout never cancels a save already accepted by the application.
func (c *ControlClient) Exchange(
	ctx context.Context,
	instance run.Identity,
	token, operationID, action string,
) (ControlOutcome, error) {
	var outcome ControlOutcome
	if _, ok := ctx.Deadline(); !ok {
		bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		ctx = bounded
	}
	request := control.Request{
		ProtocolVersion: "1.0",
		RunID:           instance.ControlID,
		OperationID:     operationID,
		Action:          action,
		Token:           token,
	}
	encoded, _ := json.Marshal(request)
	if _, err := control.DecodeRequest(encoded); err != nil {
		return outcome, problem.New("CONTROL_REQUEST_INVALID", "")
	}
	connection, err := c.dial(ctx, instance)
	if err != nil {
		return outcome, problem.New("CONTROL_CONNECTION_FAILED", "")
	}
	defer connection.Close()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(15 * time.Second)
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return outcome, problem.New("CONTROL_CONNECTION_FAILED", "")
	}
	stopClosing := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopClosing()
	if _, err := connection.Write(append(encoded, '\n')); err != nil {
		return outcome, problem.New("CONTROL_DISCONNECTED", "")
	}
	reader := bufio.NewReaderSize(connection, control.MaxFrame+1)
	for {
		frame, err := reader.ReadSlice('\n')
		if len(frame) > control.MaxFrame || err == bufio.ErrBufferFull {
			return outcome, problem.New("CONTROL_FRAME_LIMIT", "")
		}
		if err != nil {
			if ctx.Err() != nil {
				return outcome, problem.New("CONTROL_WAIT_INCOMPLETE", "")
			}
			return outcome, problem.New("CONTROL_DISCONNECTED", "")
		}
		response, err := control.DecodeResponse(frame, instance.ControlID, operationID)
		if err != nil {
			return outcome, problem.New("CONTROL_RESPONSE_INVALID", "")
		}
		outcome.State = response.State
		if response.Error != nil {
			outcome.ErrorCode = response.Error.Code
		}
		if response.Kind == "accepted" {
			outcome.Accepted = true
		}
		if response.Kind == "completed" {
			outcome.Completed = action == "shutdown" && response.State == "stopped" && response.Error == nil
		}
		if c.observe != nil {
			c.observe(outcome)
		}
		switch response.Kind {
		case "accepted":
		case "completed":
			return outcome, nil
		case "rejected":
			return outcome, nil
		case "status":
			if action == "status" {
				return outcome, nil
			}
		}
	}
}

func dialControlPipe(ctx context.Context, instance run.Identity) (net.Conn, error) {
	return dialVerifiedPipe(ctx, instance, func(pid uint32) error {
		if pid != instance.PID {
			return problem.New("INSTANCE_IDENTITY_MISMATCH", "")
		}
		process, err := openOriginalProcess(instance)
		if err != nil {
			return err
		}
		windows.CloseHandle(process)
		return nil
	})
}

// The native adapter may authenticate an owned child server. The pipe's actual
// server is opened and checked against the retained original Job before sending
// a token; process names, parent PID hints and portable IDs provide no authority.
func dialOwnedControlPipe(ctx context.Context, instance *nativeInstance) (net.Conn, error) {
	return dialVerifiedPipe(ctx, instance.identity, func(pid uint32) error {
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
		if err != nil {
			return problem.New("INSTANCE_IDENTITY_MISMATCH", "")
		}
		defer windows.CloseHandle(handle)
		status, err := windows.WaitForSingleObject(handle, 0)
		if err != nil || status != uint32(windows.WAIT_TIMEOUT) {
			return problem.New("INSTANCE_IDENTITY_MISMATCH", "")
		}
		var belongs int32
		ok, _, _ := isProcessInJob.Call(uintptr(handle), uintptr(instance.job), uintptr(unsafe.Pointer(&belongs)))
		if ok == 0 || belongs == 0 {
			return problem.New("INSTANCE_IDENTITY_MISMATCH", "")
		}
		return nil
	})
}

func dialVerifiedPipe(ctx context.Context, instance run.Identity, verify func(uint32) error) (net.Conn, error) {
	if !control.ValidIdentity(instance.ControlID) || instance.PID == 0 {
		return nil, problem.New("INSTANCE_IDENTITY_INVALID", "")
	}
	connection, err := winio.DialPipeContext(ctx, `\\.\pipe\LocalProjectHub.`+instance.ControlID)
	if err != nil {
		return nil, err
	}
	handle, ok := connection.(interface{ Fd() uintptr })
	var serverPID uint32
	if !ok || windows.GetNamedPipeServerProcessId(windows.Handle(handle.Fd()), &serverPID) != nil {
		connection.Close()
		return nil, problem.New("INSTANCE_IDENTITY_MISMATCH", "")
	}
	if err := verify(serverPID); err != nil {
		connection.Close()
		return nil, err
	}
	return connection, nil
}
