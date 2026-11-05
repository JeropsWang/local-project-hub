package environment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// TestMain provides controlled native probe executables without using a real SDK.
func TestMain(m *testing.M) {
	switch filepath.Base(os.Args[0]) {
	case "probe-node.exe":
		fmt.Print(`{"version":"22.19.0","architecture":"amd64"}`)
		os.Exit(0)
	case "probe-flood.exe":
		fmt.Print(strings.Repeat("x", 128*1024))
		os.Exit(0)
	case "probe-hang.exe":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func executable(t *testing.T, name string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(file, data, 0700); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestDiscoverParsesFixedReadOnlyProbe(t *testing.T) {
	probe := NewProbe()
	got, err := probe.Discover(context.Background(), ports.ProbeRequest{Tool: "node", Executable: executable(t, "probe-node.exe")})
	if err != nil || got.Version != "22.19.0" || got.Architecture != "amd64" {
		t.Fatalf("observation: %+v %v", got, err)
	}
	if got.Evidence.Kind != "observation" {
		t.Fatal("version probe promoted to runtime test")
	}
}

func TestTimeoutOutputCapAndRelocatedRoot(t *testing.T) {
	probe := NewProbe()
	for _, name := range []string{"probe-hang.exe", "probe-flood.exe"} {
		started := time.Now()
		timeout := 150 * time.Millisecond
		if name == "probe-flood.exe" {
			timeout = 5 * time.Second
		}
		got, err := probe.Discover(context.Background(), ports.ProbeRequest{Tool: "node", Executable: executable(t, name), Timeout: timeout})
		if err != nil {
			t.Fatal(err)
		}
		if name == "probe-hang.exe" && got.ReasonCode != "PROBE_TIMEOUT" {
			t.Fatalf("timeout: %+v", got)
		}
		if name == "probe-flood.exe" && got.ReasonCode != "PROBE_OUTPUT_LIMIT" {
			t.Fatalf("output cap: %+v", got)
		}
		if got.OutputBytes > 64*1024 || time.Since(started) > timeout+2*time.Second {
			t.Fatal("unbounded probe")
		}
	}
}

func TestProbeRejectsProjectScriptsAndCustomArguments(t *testing.T) {
	probe := NewProbe()
	for _, req := range []ports.ProbeRequest{{Tool: "npm-start"}, {Tool: "node", Args: []string{"project.mjs"}}} {
		if _, err := probe.Discover(context.Background(), req); err == nil {
			t.Fatal("untrusted execution accepted")
		}
	}
}
