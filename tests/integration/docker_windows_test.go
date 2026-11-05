package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/docker"
	"github.com/JeropsWang/local-project-hub/internal/adapters/environment"
	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
	nativeprocess "github.com/JeropsWang/local-project-hub/internal/adapters/process"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func localDockerFixture(t *testing.T) (string, string) {
	t.Helper()
	executable, err := exec.LookPath("docker.exe")
	if err != nil {
		t.Skip("local Docker CLI is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "context", "inspect", "--format", "{{json .Endpoints.docker.Host}}")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	data, err := command.Output()
	if err != nil {
		t.Fatal("cannot inspect current Docker endpoint", err)
	}
	var host string
	if json.Unmarshal(data, &host) != nil {
		t.Fatal("unexpected Docker endpoint descriptor")
	}
	const prefix = "npipe:////./pipe/"
	if !strings.HasPrefix(host, prefix) {
		t.Skip("current Docker context is not a local named-pipe endpoint")
	}
	return executable, `\\.\pipe\` + strings.TrimPrefix(host, prefix)
}

// TestLocalDockerConfigurationSmoke performs only compose config on synthetic
// temporary inputs. It does not create containers, pull images, or execute hooks.
func TestLocalDockerConfigurationSmoke(t *testing.T) {
	if os.Getenv("LPH_DOCKER_READONLY_SMOKE") != "1" {
		t.Skip("set LPH_DOCKER_READONLY_SMOKE=1 for local readonly Docker checks")
	}
	executable, pipe := localDockerFixture(t)
	root, source := t.TempDir(), t.TempDir()
	repo, err := persistence.NewFactory().Initialize(context.Background(), root, "readonly Docker smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	runner, err := docker.NewComposeRunner(executable, nativeprocess.NewToolRunner())
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := docker.NewContainerAdapter(root, map[string]string{"local": pipe}, nil, runner)
	if err != nil {
		t.Fatal(err)
	}
	base, override := filepath.Join(source, "base.json"), filepath.Join(source, "override.json")
	if err := os.WriteFile(base, []byte(`{"services":{"app":{"image":"alpine:3.22","environment":{"FIXTURE":"${FIXTURE_SECRET:-example}"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte(`{"services":{"app":{"stop_signal":"SIGTERM"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	preview, err := adapter.PreviewCompose(
		ctx,
		ports.ComposeSpec{
			ResourceID:       "99999999-9999-4999-8999-999999999999",
			Engine:           "local",
			ExpectedEngineID: "offline-config-only",
			WorkingDir:       source,
			Files:            []ports.ComposeFile{{Path: base}, {Path: override}},
			Services:         []string{"app"},
		},
	)
	if err != nil || len(preview.Fingerprint) != 64 || preview.Spec.Files[0].Path != base ||
		preview.Spec.Files[1].Path != override {
		t.Fatalf("actual Compose config smoke: %v", err)
	}
	repeated, err := adapter.PreviewCompose(ctx, preview.Spec)
	if err != nil || repeated.Fingerprint != preview.Fingerprint {
		t.Fatal("config preview fingerprint unstable", err)
	}
	t.Log("verified local Compose config flags, ordered inputs and stable fingerprint; no container mutations")
}

// TestLocalDockerInventorySmoke observes the current local engine only. Control
// remains ungranted even if a real container has familiar names or Compose labels.
func TestLocalDockerInventorySmoke(t *testing.T) {
	if os.Getenv("LPH_DOCKER_READONLY_SMOKE") != "1" {
		t.Skip("set LPH_DOCKER_READONLY_SMOKE=1 for local readonly Docker checks")
	}
	_, pipe := localDockerFixture(t)
	adapter, err := docker.NewContainerAdapter(t.TempDir(), map[string]string{"local": pipe}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	observed, err := adapter.Inventory(ctx, "local")
	if err != nil {
		t.Fatal("local readonly Engine inventory", err)
	}
	for _, item := range observed {
		if item.Identity.ControlID != "" {
			t.Fatal("readonly inventory granted control")
		}
	}
	t.Logf("observed %d local containers without granting control or modifying instances", len(observed))
}

func TestLocalDockerComposeVersionProbe(t *testing.T) {
	if os.Getenv("LPH_DOCKER_READONLY_SMOKE") != "1" {
		t.Skip("set LPH_DOCKER_READONLY_SMOKE=1 for local readonly Docker checks")
	}
	executable, _ := localDockerFixture(t)
	observation, err := environment.NewProbe().
		Discover(context.Background(), ports.ProbeRequest{Tool: "compose", Executable: executable})
	if err != nil || observation.Version == "" {
		t.Fatalf("installed Compose not discovered: reason=%s error=%v", observation.ReasonCode, err)
	}
}
