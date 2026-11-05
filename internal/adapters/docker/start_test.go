package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type composeFixtureRunner struct {
	config string
	up     func(ports.ComposeInvocation) error
	calls  [][]string
}

func (r *composeFixtureRunner) RunCompose(ctx context.Context, invocation ports.ComposeInvocation) ([]byte, error) {
	r.calls = append(r.calls, slices.Clone(invocation.Arguments))
	if slices.Contains(invocation.Arguments, "config") {
		return []byte(r.config), nil
	}
	if slices.Contains(invocation.Arguments, "up") {
		return nil, r.up(invocation)
	}
	return nil, problem.New("COMPOSE_ACTION_UNSUPPORTED", "")
}

func TestComposeStartRecordsPartialAcquisitionsWithoutRecreateOrPull(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "partial"}[partial], func(t *testing.T) {
			ctx := context.Background()
			workspace, source := t.TempDir(), t.TempDir()
			repo, err := persistence.NewFactory().Initialize(ctx, workspace, "compose start")
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			var created atomic.Bool
			labels := map[string]string{
				"com.docker.compose.project": "lph-22222222222242228222222222222222",
				"com.docker.compose.service": "database",
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/info"):
					_ = json.NewEncoder(w).Encode(map[string]string{"ID": "fixture-engine", "OSType": "linux"})
				case strings.HasSuffix(r.URL.Path, "/containers/json"):
					items := []map[string]any{}
					if created.Load() {
						items = append(items, map[string]any{"Id": fixtureContainer, "Image": "postgres:18"})
					}
					_ = json.NewEncoder(w).Encode(items)
				default:
					_ = json.NewEncoder(w).
						Encode(map[string]any{"Id": fixtureContainer, "Name": "/fixture", "Created": "created", "Config": map[string]any{"Labels": labels}, "State": map[string]any{"Running": true, "StartedAt": "started", "Status": "running"}})
				}
			}))
			defer server.Close()
			authority := repo.(ports.ContainerAuthorization)
			adapter := testAdapter(server, authority)
			adapter.workspace = workspace
			runner := &composeFixtureRunner{
				config: `{"services":{"database":{"image":"postgres:18","environment":{"PASSWORD":"fixture-private-value"}}}}`,
			}
			adapter.runner = runner
			input := filepath.Join(source, "compose.json")
			if err := os.WriteFile(input, []byte(`{"services":{"database":{"image":"postgres:18"}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			preview, err := adapter.PreviewCompose(
				ctx,
				ports.ComposeSpec{
					ResourceID:       fixtureResource,
					Engine:           "desktop",
					ExpectedEngineID: "fixture-engine",
					WorkingDir:       source,
					Files:            []ports.ComposeFile{{Path: input}},
					Services:         []string{"database"},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			spec := preview.Spec
			spec.ControlGrant = fixtureGrant
			data, _ := json.Marshal(preview)
			if strings.Contains(string(data), "fixture-private-value") {
				t.Fatal("raw config exposed in preview")
			}
			record := resource.Record{
				SchemaVersion:     "1.0",
				ResourceID:        fixtureResource,
				Control:           "managed",
				Engine:            spec.Engine,
				EngineID:          spec.ExpectedEngineID,
				ComposeProject:    preview.ProjectName,
				WorkingDir:        source,
				Files:             []resource.File{{Path: input, SHA256: spec.Files[0].SHA256}},
				Services:          spec.Services,
				ConfigFingerprint: preview.Fingerprint,
				ControlGrant:      fixtureGrant,
			}
			store := repo.(ports.ResourceRepository)
			if _, err := store.SaveResource(ctx, record, ""); err != nil {
				t.Fatal(err)
			}
			runner.up = func(invocation ports.ComposeInvocation) error {
				current, err := store.Resource(ctx, fixtureResource)
				if err != nil || current.PendingStart == "" {
					t.Fatal("CLI effect began without durable intent", err)
				}
				args := invocation.Arguments
				if !slices.Contains(args, "--no-recreate") || !slices.Contains(args, "--no-build") ||
					!strings.Contains(strings.Join(args, " "), "--pull never") {
					t.Fatal("unsafe implicit Compose effects", args)
				}
				var overlayName string
				for i := 0; i < len(args)-1; i++ {
					if args[i] == "--file" {
						overlayName = args[i+1]
					}
				}
				overlay, err := os.ReadFile(overlayName)
				if err != nil {
					t.Fatal(err)
				}
				var value struct {
					Services map[string]struct{ Labels map[string]string }
				}
				if json.Unmarshal(overlay, &value) != nil {
					t.Fatal("invalid controller overlay")
				}
				for key, value := range value.Services["database"].Labels {
					labels[key] = value
				}
				created.Store(true)
				if partial {
					return problem.New("COMPOSE_EXECUTION_FAILED", "")
				}
				return nil
			}
			instances, err := adapter.Start(ctx, spec)
			if (err != nil) != partial || len(instances) != 1 {
				t.Fatalf("partial acquisitions lost: %+v %v", instances, err)
			}
			if err := authority.AuthorizeContainer(ctx, instances[0]); err != nil {
				t.Fatal("new exact acquisition not bound", err)
			}
			current, err := store.Resource(ctx, fixtureResource)
			if err != nil || (current.PendingStart != "") != partial || len(current.Instances) != 1 {
				t.Fatal("durable intent not resolved", err)
			}
		})
	}
}

func TestComposePreviewBlocksImplicitHooksAndMissingSelectedDependencies(t *testing.T) {
	workspace, source := t.TempDir(), t.TempDir()
	repo, err := persistence.NewFactory().Initialize(context.Background(), workspace, "compose preview")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	file := filepath.Join(source, "compose.json")
	if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ config, code string }{
		{`{"services":{"api":{"pre_stop":[{"command":"save"}]}}}`, "COMPOSE_HOOKS_REQUIRE_REVIEW"},
		{`{"services":{"api":{"depends_on":{"database":{"condition":"service_healthy"}}},"database":{}}}`, "COMPOSE_DEPENDENCY_NOT_SELECTED"},
	} {
		adapter := newContainerAdapter(nil, nil)
		adapter.workspace = workspace
		adapter.runner = &composeFixtureRunner{config: tc.config}
		_, err := adapter.PreviewCompose(
			context.Background(),
			ports.ComposeSpec{
				ResourceID:       fixtureResource,
				Engine:           "desktop",
				ExpectedEngineID: "fixture-engine",
				WorkingDir:       source,
				Files:            []ports.ComposeFile{{Path: file}},
				Services:         []string{"api"},
			},
		)
		if err == nil || err.Error() != tc.code {
			t.Fatalf("implicit hook/dependency accepted: %v", err)
		}
	}
}
