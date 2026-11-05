// controlapp is a synthetic managed application used only by process integration tests.
// It has no database, network API or WeChat dependency.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/JeropsWang/local-project-hub/internal/domain/control"
)

func main() {
	mode := flag.String("mode", "normal", "synthetic scenario")
	exitCode := flag.Int("exit-code", 0, "synthetic child exit code")
	signal := flag.String("signal", "", "immutable snapshot used by command stop")
	flag.Parse()
	if *signal != "" {
		data, err := os.ReadFile(*signal)
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile("shutdown.request", data, 0600); err != nil {
			os.Exit(2)
		}
		_ = os.WriteFile("snapshot-name.txt", []byte(*signal), 0600)
		return
	}
	if *mode == "child-worker" {
		_ = os.WriteFile("child-ready", []byte("synthetic child"), 0600)
		waitRelease()
		os.Exit(*exitCode)
	}
	if *mode == "parent-exit" || *mode == "completed-with-child" || *mode == "completed-with-crashing-child" {
		exe, _ := os.Executable()
		child := exec.Command(exe, "--mode", "child-worker")
		if *mode == "completed-with-crashing-child" {
			child.Args = append(child.Args, "--exit-code", "7")
		}
		child.Env = os.Environ()
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		if *mode == "parent-exit" {
			return
		}
	}
	if *mode == "script-stop" {
		fmt.Println("known secret:", os.Getenv("FAKE_SECRET"))
		_ = os.WriteFile("ready", []byte("synthetic script app"), 0600)
		for {
			if data, err := os.ReadFile("shutdown.request"); err == nil {
				_ = os.WriteFile("saved.txt", data, 0600)
				return
			}
			if released() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	runID, token, pipe := os.Getenv("LPH_RUN_ID"), os.Getenv("LPH_CONTROL_TOKEN"), os.Getenv("LPH_CONTROL_PIPE")
	if !control.ValidIdentity(runID) || !control.ValidToken(token) || pipe != `\\.\pipe\LocalProjectHub.`+runID {
		os.Exit(2)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		os.Exit(2)
	}
	listener, err := winio.ListenPipe(
		pipe,
		&winio.PipeConfig{
			SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;" + user.User.Sid.String() + ")",
			InputBufferSize:    control.MaxFrame,
			OutputBufferSize:   control.MaxFrame,
		},
	)
	if err != nil {
		os.Exit(2)
	}
	defer listener.Close()
	go func() { waitRelease(); os.Exit(0) }()
	_ = os.WriteFile("ready", []byte("synthetic pipe app"), 0600)
	var mu sync.Mutex
	operations := map[string]control.Request{}
	responses := map[string]control.Response{}
	currentState, activeShutdown := "running", ""
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go func(connection net.Conn) {
			defer connection.Close()
			line, err := bufio.NewReaderSize(connection, control.MaxFrame+1).ReadSlice('\n')
			if err != nil || len(line) > control.MaxFrame {
				return
			}
			request, err := control.DecodeRequest(line)
			if err != nil {
				return
			}
			response := control.Response{
				ProtocolVersion: "1.0",
				RunID:           runID,
				OperationID:     request.OperationID,
				Kind:            "status",
				State:           "running",
			}
			write := func() { bytes, _ := json.Marshal(response); _, _ = connection.Write(append(bytes, '\n')) }
			expected := token
			if *mode == "wrong-token" {
				expected = strings.Repeat("f", 64)
			}
			if request.RunID != runID || !control.Authenticate(request.Token, expected) {
				response.Kind = "rejected"
				response.Error = &control.Error{Code: "UNAUTHORIZED", Message: "Synthetic authentication failed."}
				write()
				return
			}
			mu.Lock()
			previous, seen := operations[request.OperationID]
			if seen && previous.Action != request.Action {
				mu.Unlock()
				response.Kind = "rejected"
				response.Error = &control.Error{Code: "OPERATION_CONFLICT", Message: "Synthetic operation mismatch."}
				write()
				return
			}
			if seen {
				response = responses[request.OperationID]
				mu.Unlock()
				write()
				return
			}
			operations[request.OperationID] = request
			response.State = currentState
			if request.Action == "status" {
				responses[request.OperationID] = response
				mu.Unlock()
				write()
				return
			}
			if request.Action == "quiesce" && activeShutdown == "" {
				response.Kind = "completed"
				response.State = "quiescing"
				currentState = response.State
				responses[request.OperationID] = response
				mu.Unlock()
				write()
				return
			}
			if activeShutdown != "" {
				response.Kind = "rejected"
				response.State = "blocked"
				response.Error = &control.Error{Code: "WORK_PENDING", Message: "Synthetic stop already in progress."}
				responses[request.OperationID] = response
				mu.Unlock()
				write()
				return
			}
			if *mode == "blocked" || *mode == "save-failed" {
				response.Kind = "rejected"
				response.State = "blocked"
				response.Error = &control.Error{Code: "WORK_PENDING", Message: "Synthetic work is pending."}
				if *mode == "save-failed" {
					response.State = "failed"
					response.Error.Code = "SAVE_FAILED"
				}
				currentState = response.State
				responses[request.OperationID] = response
				mu.Unlock()
				write()
				return
			}
			response.Kind = "accepted"
			response.State = "saving"
			currentState = response.State
			activeShutdown = request.OperationID
			responses[request.OperationID] = response
			mu.Unlock()
			write()
			if *mode == "ack-crash" {
				os.Exit(3)
			}
			if *mode == "slow-save" {
				time.Sleep(600 * time.Millisecond)
			}
			if err := os.WriteFile("saved.txt", []byte("synthetic save completed"), 0600); err != nil {
				return
			}
			response.Kind = "completed"
			response.State = "stopped"
			mu.Lock()
			currentState = response.State
			responses[request.OperationID] = response
			mu.Unlock()
			write()
			// Byte-mode pipes wait for their queued writes before process exit; the
			// client still verifies actual exit of the original root and children.
			if pipe, ok := connection.(interface{ Flush() error }); ok {
				_ = pipe.Flush()
			}
			time.Sleep(30 * time.Millisecond)
			os.Exit(0)
		}(connection)
	}
}

func released() bool { _, err := os.Stat("release"); return err == nil }
func waitRelease() {
	for !released() {
		time.Sleep(10 * time.Millisecond)
	}
}
