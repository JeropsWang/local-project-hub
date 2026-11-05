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

	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

func TestInventoryDoesNotAdoptComposeLabelsOrExposeEnvironment(t *testing.T) {
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations.Add(1)
			w.WriteHeader(500)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/info"):
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": "fixture-engine", "OSType": "linux"})
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			_ = json.NewEncoder(w).
				Encode([]map[string]any{{"Id": fixtureContainer, "Names": []string{"/existing-container"}, "Image": "postgres:18", "Labels": map[string]string{"com.docker.compose.project": "lph-22222222222242228222222222222222"}}})
		default:
			_ = json.NewEncoder(w).
				Encode(map[string]any{"Id": fixtureContainer, "Created": "2026-01-01T00:00:00Z", "Name": "/existing-container", "Image": "sha256:fixture", "Config": map[string]any{"Env": []string{"PASSWORD=fixture-never-return"}, "Labels": map[string]string{"com.docker.compose.project": "lph-22222222222242228222222222222222", "com.docker.compose.service": "database", "com.docker.compose.project.config_files": "/fixture/old-compose.yml", "com.docker.compose.project.working_dir": "/fixture/old-checkout"}}, "State": map[string]any{"Running": true, "Status": "running", "StartedAt": "2026-01-01T00:01:00Z", "Health": map[string]string{"Status": "healthy"}}})
		}
	}))
	defer server.Close()
	observations, err := testAdapter(server, &fixtureAuthorization{}).Inventory(context.Background(), "desktop")
	if err != nil || len(observations) != 1 {
		t.Fatalf("inventory: %+v %v", observations, err)
	}
	item := observations[0]
	if item.Identity.ControlID != "" || item.ComposeService != "database" ||
		item.WorkingDir != "/fixture/old-checkout" ||
		item.Health != "healthy" ||
		mutations.Load() != 0 {
		t.Fatalf("inventory granted control or lost source evidence: %+v", item)
	}
	data, _ := json.Marshal(observations)
	if strings.Contains(string(data), "fixture-never-return") {
		t.Fatal("container environment exposed")
	}
}

func TestUnsafeConfiguredSignalsNeverPosted(t *testing.T) {
	for _, signal := range []string{"SIGKILL", "KILL", "9", "SIG9", "SIG0009", "SIGSTOP", "19", "SIG19"} {
		t.Run(signal, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					w.WriteHeader(204)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/info") {
					_ = json.NewEncoder(w).Encode(map[string]string{"ID": "fixture-engine", "OSType": "linux"})
					return
				}
				_ = json.NewEncoder(w).
					Encode(map[string]any{"Id": fixtureContainer, "Created": "created", "Config": map[string]string{"StopSignal": signal}, "State": map[string]any{"Running": true, "StartedAt": "started"}})
			}))
			defer server.Close()
			adapter := testAdapter(server, &fixtureAuthorization{allowed: true})
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			_, err := adapter.Stop(
				ctx,
				run.Identity{
					Kind:         "container",
					Engine:       "desktop",
					EngineID:     "fixture-engine",
					ContainerID:  fixtureContainer,
					CreationTime: "created",
					StartedAt:    "started",
					ControlID:    fixtureGrant,
				},
			)
			if err == nil || err.Error() != "CONTAINER_STOP_SIGNAL_UNSAFE" || posts.Load() != 0 {
				t.Fatalf("unsafe signal alias accepted: posted=%d error=%v", posts.Load(), err)
			}
		})
	}
}
