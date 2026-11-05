package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	nativeprocess "github.com/JeropsWang/local-project-hub/internal/adapters/process"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// TestMain supplies a synthetic Docker CLI without loading any project or real daemon.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "--host" {
		if strings.Contains(os.Args[2], "fixture-flood") {
			fmt.Print(strings.Repeat("x", 17*1024*1024))
			os.Exit(0)
		}
		data, _ := json.Marshal(
			map[string]any{
				"args":          os.Args[1:],
				"parentSecret":  os.Getenv("FAKE_PARENT_SECRET"),
				"dockerContext": os.Getenv("DOCKER_CONTEXT"),
			},
		)
		fmt.Print(string(data))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestNativeComposeRunnerUsesArgvAndBoundsOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewComposeRunner(executable, nativeprocess.NewToolRunner())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PARENT_SECRET", "fixture-private-value")
	t.Setenv("DOCKER_CONTEXT", "wrong-inherited-context")
	spec := ports.ComposeSpec{
		ResourceID: fixtureResource,
		WorkingDir: t.TempDir(),
		Files:      []ports.ComposeFile{{Path: `D:\fixture\base.yml`}},
		Services:   []string{"database"},
	}
	args, err := ComposeArguments(spec, "config")
	if err != nil {
		t.Fatal(err)
	}
	data, err := runner.RunCompose(
		context.Background(),
		ports.ComposeInvocation{Pipe: `\\.\pipe\fixture`, Arguments: args, CWD: spec.WorkingDir},
	)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Args          []string
		ParentSecret  string
		DockerContext string
	}
	if json.Unmarshal(data, &report) != nil || len(report.Args) < 3 || report.Args[0] != "--host" ||
		report.Args[1] != "npipe:////./pipe/fixture" ||
		report.ParentSecret != "" ||
		report.DockerContext != "" {
		t.Fatal("wrong CLI environment or argv")
	}
	_, err = runner.RunCompose(
		context.Background(),
		ports.ComposeInvocation{Pipe: `\\.\pipe\fixture-flood`, Arguments: args, CWD: spec.WorkingDir},
	)
	if err == nil || err.Error() != "COMPOSE_OUTPUT_LIMIT" {
		t.Fatalf("output flood not bounded: %v", err)
	}
	_, err = runner.RunCompose(
		context.Background(),
		ports.ComposeInvocation{
			Pipe:      `\\.\pipe\fixture`,
			Arguments: []string{"compose", "down", "-v"},
			CWD:       spec.WorkingDir,
		},
	)
	if err == nil || err.Error() != "COMPOSE_ACTION_UNSUPPORTED" {
		t.Fatalf("destructive CLI command accepted: %v", err)
	}
}
