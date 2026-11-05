package process

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/JeropsWang/local-project-hub/internal/domain/preparation"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func preparationFixture(t *testing.T) (string, ports.PreparationCommand) {
	t.Helper()
	workspace := t.TempDir()
	root := filepath.Join(workspace, "envs", "fixture")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(preparation.Receipt{Root: root, State: "preparing"})
	if err := os.WriteFile(filepath.Join(root, ".lph-environment.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return workspace, ports.PreparationCommand{
		Executable:  executable,
		Args:        []string{"-test.run=^TestPreparationChild$"},
		CWD:         root,
		OwnedRoot:   root,
		Timeout:     10 * time.Second,
		Environment: map[string]string{"LPH_PREPARATION_FIXTURE": "inspect"},
	}
}

func TestPreparationChild(t *testing.T) {
	mode := os.Getenv("LPH_PREPARATION_FIXTURE")
	if mode == "" {
		return
	}
	if mode == "inspect" {
		_ = os.WriteFile(
			"environment.txt",
			[]byte(os.Getenv("USERPROFILE")+"\n"+os.Getenv("FAKE_PARENT_SECRET")+"\n"+os.Getenv("NODE_OPTIONS")),
			0600,
		)
		fmt.Print(strings.Repeat("fixture-output", 100000))
		return
	}
	if mode == "parent" {
		exe, _ := os.Executable()
		child := exec.Command(exe, "-test.run=^TestPreparationChild$")
		child.Env = []string{"LPH_PREPARATION_FIXTURE=child", "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}
		child.Dir, _ = os.Getwd()
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		_ = os.WriteFile("child.pid", []byte(strconv.Itoa(child.Process.Pid)), 0600)
	}
	for {
		time.Sleep(time.Second)
	}
}

// Inheriting parent secrets or unbounded stdout would break observable isolation/logging.
func TestPreparationRunnerUsesPrivateHomeAndBoundsLog(t *testing.T) {
	workspace, command := preparationFixture(t)
	t.Setenv("FAKE_PARENT_SECRET", "fixture-never-inherit")
	t.Setenv("NODE_OPTIONS", "fixture-never-inherit")
	runner := NewPreparationRunner(workspace)
	result, err := runner.Run(context.Background(), command)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("run: %+v %v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(command.CWD, "environment.txt"))
	if err != nil || strings.Contains(string(data), "fixture-never-inherit") ||
		!strings.HasPrefix(string(data), command.OwnedRoot) {
		t.Fatalf("parent environment leaked: %q %v", data, err)
	}
	info, err := os.Stat(result.LogFile)
	if err != nil || info.Size() > 1024*1024 {
		t.Fatalf("unbounded log: %v %v", info, err)
	}
}

// Root and grandchildren must be assigned before execution, and all owned children end on cancel.
func TestCancelledPreparationTerminatesOnlyItsOwnedProcessTree(t *testing.T) {
	workspace, command := preparationFixture(t)
	command.Environment["LPH_PREPARATION_FIXTURE"] = "parent"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := NewPreparationRunner(workspace).Run(ctx, command); done <- err }()
	var childID int
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(filepath.Join(command.CWD, "child.pid"))
		childID, _ = strconv.Atoi(string(data))
		if childID > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childID == 0 {
		cancel()
		t.Fatalf("no child: %v", <-done)
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(childID))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	cancel()
	select {
	case err := <-done:
		if err == nil || ctx.Err() != context.Canceled {
			t.Fatalf("cancel result: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel blocked")
	}
	status, err := windows.WaitForSingleObject(handle, 3000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("owned child survived cancellation: %d %v", status, err)
	}
}

func TestPreparationRunnerRejectsOriginalCWD(t *testing.T) {
	workspace, command := preparationFixture(t)
	command.CWD = t.TempDir()
	_, err := NewPreparationRunner(workspace).Run(context.Background(), command)
	if err == nil || err.Error() != "PREPARATION_DIRECTORY_UNOWNED" {
		t.Fatalf("original cwd accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(command.CWD, "environment.txt")); !os.IsNotExist(err) {
		t.Fatal("executed in original")
	}
}
