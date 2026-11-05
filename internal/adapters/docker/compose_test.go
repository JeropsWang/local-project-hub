package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

const fixtureResource = "22222222-2222-4222-8222-222222222222"
const fixtureContainer = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const fixtureGrant = "33333333-3333-4333-8333-333333333333"

type fixtureAuthorization struct {
	allowed   bool
	attempted atomic.Bool
}

func (a *fixtureAuthorization) AuthorizeContainer(context.Context, run.Identity) error {
	if !a.allowed {
		return problem.New("RESOURCE_CONTROL_NOT_GRANTED", "")
	}
	return nil
}
func (a *fixtureAuthorization) BeginContainerStop(context.Context, run.Identity, string) (bool, error) {
	return a.attempted.CompareAndSwap(false, true), nil
}

func TestComposeIdentityAndFileOrder(t *testing.T) {
	project, err := ComposeProject(fixtureResource)
	if err != nil || project != "lph-22222222222242228222222222222222" {
		t.Fatalf("unstable project name: %s %v", project, err)
	}
	spec := ports.ComposeSpec{
		ResourceID:       fixtureResource,
		Engine:           "desktop",
		ExpectedEngineID: "engine-fixture",
		Files:            []ports.ComposeFile{{Path: `D:\fixture\base.yml`}, {Path: `D:\fixture\override.yml`}},
		WorkingDir:       `D:\fixture`,
		Services:         []string{"database", "cache"},
	}
	args, err := ComposeArguments(spec, "config")
	if err != nil {
		t.Fatal(err)
	}
	files := []string{}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--file" {
			files = append(files, args[i+1])
		}
	}
	if strings.Join(files, ",") != `D:\fixture\base.yml,D:\fixture\override.yml` {
		t.Fatalf("compose order changed: %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "--no-interpolate") ||
		!strings.Contains(strings.Join(args, " "), "--no-env-resolution") {
		t.Fatal("config inspection can expand secrets", args)
	}
	spec.ProjectName = "existing-user-project"
	if _, err := ComposeArguments(spec, "up"); err == nil || err.Error() != "RESOURCE_CONTROL_NOT_GRANTED" {
		t.Fatalf("existing project adopted without grant: %v", err)
	}
}

func TestExternalResourcesAndStopTimeout(t *testing.T) {
	var signals, force, deleteCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/info"):
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": "engine-fixture", "OSType": "linux"})
		case strings.HasSuffix(r.URL.Path, "/json"):
			_ = json.NewEncoder(w).
				Encode(map[string]any{"Id": fixtureContainer, "Created": "2026-01-01T00:00:00Z", "Config": map[string]any{"StopSignal": "SIGTERM", "Labels": map[string]string{}}, "State": map[string]any{"Running": true, "StartedAt": "2026-01-01T00:01:00Z", "ExitCode": 0}})
		case strings.HasSuffix(r.URL.Path, "/kill"):
			signals.Add(1)
			if r.URL.Query().Get("signal") != "SIGTERM" {
				force.Add(1)
			}
			w.WriteHeader(204)
		default:
			if r.Method == http.MethodDelete {
				deleteCalls.Add(1)
			}
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	grant := &fixtureAuthorization{}
	adapter := testAdapter(server, grant)
	instance := run.Identity{
		Kind:         "container",
		Engine:       "desktop",
		EngineID:     "engine-fixture",
		ContainerID:  fixtureContainer,
		CreationTime: "2026-01-01T00:00:00Z",
		StartedAt:    "2026-01-01T00:01:00Z",
		ControlID:    fixtureGrant,
	}
	if _, err := adapter.Stop(context.Background(), instance); err == nil ||
		err.Error() != "RESOURCE_CONTROL_NOT_GRANTED" {
		t.Fatalf("reference resource controlled: %v", err)
	}
	if signals.Load() != 0 {
		t.Fatal("reference container received signal")
	}
	grant.allowed = true
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	result, err := adapter.Stop(ctx, instance)
	cancel()
	if err != nil || result.Graceful || result.Code != "CONTAINER_STOP_INCOMPLETE" {
		t.Fatalf("timeout treated as normal stop: %+v %v", result, err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err = adapter.Stop(ctx, instance)
	cancel()
	if err != nil || signals.Load() != 1 || force.Load() != 0 || deleteCalls.Load() != 0 {
		t.Fatalf("unsafe/repeated Docker effects: %d %d %d %v", signals.Load(), force.Load(), deleteCalls.Load(), err)
	}
}

func TestDockerEngineAndStartIdentityChangesBlockSignals(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/info") {
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": "different-engine", "OSType": "linux"})
			return
		}
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer server.Close()
	adapter := testAdapter(server, &fixtureAuthorization{allowed: true})
	instance := run.Identity{
		Kind:        "container",
		Engine:      "desktop",
		EngineID:    "original-engine",
		ContainerID: fixtureContainer,
		ControlID:   fixtureGrant,
	}
	if _, err := adapter.Stop(context.Background(), instance); err == nil || err.Error() != "DOCKER_ENGINE_CHANGED" {
		t.Fatalf("engine identity not pinned: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("controlled a replacement engine")
	}
}

func testAdapter(server *httptest.Server, authorization ports.ContainerAuthorization) *NativeContainerAdapter {
	return newContainerAdapter(
		map[string]engineConnection{"desktop": {baseURL: server.URL, client: server.Client()}},
		authorization,
	)
}
