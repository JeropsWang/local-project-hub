package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
	nativeprocess "github.com/JeropsWang/local-project-hub/internal/adapters/process"
	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

var helperBuild sync.Once
var controlHelper string
var helperBuildError error

func processFixture(t *testing.T, mode string) (*nativeprocess.NativeProcessAdapter, ports.ProcessSpec, string) {
	t.Helper()
	helperBuild.Do(func() {
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			helperBuildError = err
			return
		}
		controlHelper = filepath.Join(root, ".superpowers", "controlapp-fixture.exe")
		command := exec.Command("go", "build", "-o", controlHelper, "./tests/helpers/controlapp")
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			helperBuildError = fmtError(string(output), err)
		}
	})
	if helperBuildError != nil {
		t.Fatal(helperBuildError)
	}
	root := t.TempDir()
	repo, err := persistence.NewFactory().Initialize(context.Background(), root, "process fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	adapter, err := nativeprocess.NewProcessAdapter(root)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := identity.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	spec := ports.ProcessSpec{RunID: runID, UnitID: "fixture", Command: ports.ProcessCommand{Executable: controlHelper, Args: []string{"--mode", mode}, CWD: source}, Stop: ports.StopSpec{Kind: "managed-pipe"}}
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(source, "release"), []byte("fixture cleanup"), 0600)
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			if adapter.Close() == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Error("fixture process did not exit during cleanup")
	})
	return adapter, spec, root
}

func fmtError(output string, err error) error { return &helperError{text: output, err: err} }

type helperError struct {
	text string
	err  error
}

func (e *helperError) Error() string { return e.text + ": " + e.err.Error() }

func waitFile(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(name); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("fixture did not become ready", name)
}

func stopRequest() ports.StopSpec {
	op, _ := identity.NewUUID()
	return ports.StopSpec{OperationID: op, Timeout: 300 * time.Millisecond}
}

func TestPIDReuseAndParentExitAreNotCompletion(t *testing.T) {
	adapter, spec, _ := processFixture(t, "normal")
	instance, err := adapter.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(spec.Command.CWD, "ready"))
	if err := adapter.Close(); err == nil || err.Error() != "PROCESS_STILL_RUNNING" {
		t.Fatalf("closed an active business process group: %v", err)
	}
	wrong := instance
	wrong.CreationTime = "1"
	if _, err := adapter.Stop(context.Background(), wrong, stopRequest()); err == nil || err.Error() != "INSTANCE_IDENTITY_MISMATCH" {
		t.Fatalf("wrong process controlled: %v", err)
	}
	if _, err := os.Stat(filepath.Join(spec.Command.CWD, "saved.txt")); !os.IsNotExist(err) {
		t.Fatal("sent shutdown to wrong identity")
	}
	result, err := adapter.Stop(context.Background(), instance, stopRequest())
	if err != nil || !result.Graceful {
		t.Fatalf("original stop: %+v %v", result, err)
	}

	childAdapter, childSpec, _ := processFixture(t, "parent-exit")
	parent, err := childAdapter.Start(context.Background(), childSpec)
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(childSpec.Command.CWD, "child-ready"))
	deadline := time.Now().Add(3 * time.Second)
	var state ports.InstanceState
	for time.Now().Before(deadline) {
		state, err = childAdapter.Inspect(context.Background(), parent)
		if err != nil {
			t.Fatal(err)
		}
		if !state.RootRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state.RootRunning || state.ActiveProcesses == 0 || state.Exited {
		t.Fatalf("parent exit hid child: %+v", state)
	}
}

func TestNativeControlRejectsWrongTokenAndAckThenCrash(t *testing.T) {
	for _, mode := range []string{"wrong-token", "ack-crash", "save-failed", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			adapter, spec, _ := processFixture(t, mode)
			instance, err := adapter.Start(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			waitFile(t, filepath.Join(spec.Command.CWD, "ready"))
			result, err := adapter.Stop(context.Background(), instance, stopRequest())
			if err != nil || result.Graceful {
				t.Fatalf("false graceful result: %+v %v", result, err)
			}
			if mode == "wrong-token" && result.Code != "UNAUTHORIZED" {
				t.Fatalf("wrong token accepted: %+v", result)
			}
		})
	}
}

func TestCompletedResponseStillWaitsForChildrenAndContinuesSameOperation(t *testing.T) {
	adapter, spec, _ := processFixture(t, "completed-with-child")
	instance, err := adapter.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(spec.Command.CWD, "child-ready"))
	request := stopRequest()
	result, err := adapter.Stop(context.Background(), instance, request)
	if err != nil || result.Graceful || result.ActiveProcesses == 0 {
		t.Fatalf("live child considered stopped: %+v %v", result, err)
	}
	if err := os.WriteFile(filepath.Join(spec.Command.CWD, "release"), []byte("finish child"), 0600); err != nil {
		t.Fatal(err)
	}
	request.WaitOnly = true
	request.Timeout = 2 * time.Second
	result, err = adapter.Stop(context.Background(), instance, request)
	if err != nil || !result.Graceful {
		t.Fatalf("did not resume original stop: %+v %v", result, err)
	}
}

func TestCompletedRootCannotHideChildFailure(t *testing.T) {
	adapter, spec, _ := processFixture(t, "completed-with-crashing-child")
	instance, err := adapter.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(spec.Command.CWD, "child-ready"))
	request := stopRequest()
	result, err := adapter.Stop(context.Background(), instance, request)
	if err != nil || result.Graceful {
		t.Fatalf("unexpected initial result: %+v %v", result, err)
	}
	if err := os.WriteFile(filepath.Join(spec.Command.CWD, "release"), []byte("child exits nonzero"), 0600); err != nil {
		t.Fatal(err)
	}
	request.WaitOnly = true
	request.Timeout = 2 * time.Second
	result, err = adapter.Stop(context.Background(), instance, request)
	if err != nil || result.Graceful || result.Code != "PROCESS_EXIT_FAILED" {
		t.Fatalf("child failure hidden: %+v %v", result, err)
	}
}

func TestStopUsesOriginalScriptAndRedactsLogs(t *testing.T) {
	adapter, spec, root := processFixture(t, "script-stop")
	bytes := []byte("original snapshot")
	digest := sha256.Sum256(bytes)
	spec.Scripts = map[string]ports.ProcessScript{"stop": {Name: "stop.mjs", Bytes: bytes, SHA256: hex.EncodeToString(digest[:])}}
	command := ports.ProcessCommand{Executable: spec.Command.Executable, CWD: spec.Command.CWD, Args: []string{"--signal", "${file:stop}"}}
	spec.Stop = ports.StopSpec{Kind: "command", Command: &command}
	spec.Command.Environment = map[string]string{"FAKE_SECRET": "fixture-process-secret"}
	spec.SecretValues = []string{"fixture-process-secret"}
	instance, err := adapter.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(spec.Command.CWD, "ready"))
	command.Args = []string{"--signal", "future-edited-script"}
	request := stopRequest()
	request.Command = &command
	request.Timeout = 2 * time.Second
	result, err := adapter.Stop(context.Background(), instance, request)
	if err != nil || !result.Graceful {
		t.Fatalf("immutable command stop: %+v %v", result, err)
	}
	saved, err := os.ReadFile(filepath.Join(spec.Command.CWD, "saved.txt"))
	if err != nil || string(saved) != "original snapshot" {
		t.Fatal("stop used edited script", err)
	}
	name, err := os.ReadFile(filepath.Join(spec.Command.CWD, "snapshot-name.txt"))
	if err != nil || !strings.HasSuffix(string(name), "stop.mjs") {
		t.Fatal("snapshot lost interpreter extension", err)
	}
	page, err := nativeprocess.ReadRunLog(root, instance.ControlID, "0", 2000)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page.Entries {
		if strings.Contains(entry.Text, "fixture-process-secret") {
			t.Fatal("secret reached log")
		}
	}
}

func TestStopWaitTimeoutDoesNotCancelAcceptedSave(t *testing.T) {
	adapter, spec, _ := processFixture(t, "slow-save")
	instance, err := adapter.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(spec.Command.CWD, "ready"))
	request := stopRequest()
	request.Timeout = 80 * time.Millisecond
	result, err := adapter.Stop(context.Background(), instance, request)
	if err != nil || result.Graceful || result.RootExited || !result.Accepted {
		t.Fatalf("save wait incorrectly completed: %+v %v", result, err)
	}
	request.WaitOnly = true
	request.Timeout = 2 * time.Second
	result, err = adapter.Stop(context.Background(), instance, request)
	if err != nil || !result.Graceful {
		t.Fatalf("accepted save was cancelled or repeated: %+v %v", result, err)
	}
	if data, err := os.ReadFile(filepath.Join(spec.Command.CWD, "saved.txt")); err != nil || string(data) != "synthetic save completed" {
		t.Fatal("final save missing", err)
	}
}

func TestChangedRunSnapshotIsNotExecutedAsOriginalStop(t *testing.T) {
	adapter, spec, root := processFixture(t, "script-stop")
	bytes := []byte("original snapshot")
	digest := sha256.Sum256(bytes)
	spec.Scripts = map[string]ports.ProcessScript{"stop": {Name: "stop.mjs", Bytes: bytes, SHA256: hex.EncodeToString(digest[:])}}
	command := ports.ProcessCommand{Executable: spec.Command.Executable, CWD: spec.Command.CWD, Args: []string{"--signal", "${file:stop}"}}
	spec.Stop = ports.StopSpec{Kind: "command", Command: &command}
	instance, err := adapter.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(spec.Command.CWD, "ready"))
	file := filepath.Join(root, "runs", spec.RunID, "scripts", "stop", "stop.mjs")
	if err := os.WriteFile(file, []byte("changed snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Stop(context.Background(), instance, stopRequest())
	if err != nil || result.Graceful || result.Code != "RUN_SCRIPT_CHANGED" {
		t.Fatalf("tampered snapshot executed: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(spec.Command.CWD, "shutdown.request")); !os.IsNotExist(err) {
		t.Fatal("changed stop script ran")
	}
}
