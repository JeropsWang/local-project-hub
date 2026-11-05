package environment

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func runtimeWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repo, err := persistence.NewFactory().Initialize(context.Background(), root, "runtime fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

func runtimeZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, value := range entries {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func fixtureArtifact(id, version, url string, data []byte) RuntimeArtifact {
	digest := sha256.Sum256(data)
	return RuntimeArtifact{
		ID:           id,
		Component:    "python",
		Version:      version,
		Architecture: "amd64",
		ABI:          "cp" + version[:1] + version[2:4],
		URL:          url,
		SHA256:       hex.EncodeToString(digest[:]),
		Format:       "zip",
		StripPrefix:  "runtime/",
		Executable:   "python.exe",
		Source:       "synthetic TLS fixture",
		License:      "synthetic fixture",
	}
}

func TestCorruptDownloadAndCancellationLeaveOriginalUntouched(t *testing.T) {
	root := runtimeWorkspace(t)
	original := t.TempDir()
	sentinel := filepath.Join(original, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	data := runtimeZip(t, map[string]string{"runtime/python.exe": "synthetic runtime"})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte("corrupt"))
	}))
	defer server.Close()
	artifact := fixtureArtifact("python-fixture", "3.12.10", server.URL+"/corrupt", data)
	provider, err := NewRuntimeProvider(
		root,
		RuntimeCatalog{SchemaVersion: "1.0", Artifacts: []RuntimeArtifact{artifact}},
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := provider.Plan(
		context.Background(),
		ports.RuntimeRequest{
			Requirement: project.Requirement{
				Component:    "python",
				Semantics:    "pep440",
				Range:        "==3.12.10",
				Architecture: "amd64",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	pathBefore := os.Getenv("PATH")
	if _, err := provider.Install(context.Background(), plan); err == nil || err.Error() != "ARTIFACT_HASH_MISMATCH" {
		t.Fatalf("corrupt artifact: %v", err)
	}
	if _, err := os.Stat(plan.Destination); !os.IsNotExist(err) {
		t.Fatal("corrupt runtime published")
	}
	artifact.URL = server.URL + "/slow"
	provider, err = NewRuntimeProvider(
		root,
		RuntimeCatalog{SchemaVersion: "1.0", Artifacts: []RuntimeArtifact{artifact}},
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = provider.Plan(
		context.Background(),
		ports.RuntimeRequest{
			Requirement: project.Requirement{
				Component:    "python",
				Semantics:    "pep440",
				Range:        "==3.12.10",
				Architecture: "amd64",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := provider.Install(ctx, plan); err == nil {
		t.Fatal("cancelled download succeeded")
	}
	if os.Getenv("PATH") != pathBefore {
		t.Fatal("global PATH changed")
	}
	if value, err := os.ReadFile(sentinel); err != nil || string(value) != "original" {
		t.Fatal("original project changed")
	}
}

func TestTwoPythonABIsAndHardcodedEnvironment(t *testing.T) {
	root := runtimeWorkspace(t)
	data := runtimeZip(t, map[string]string{"runtime/python.exe": "synthetic runtime"})
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }),
	)
	defer server.Close()
	a := fixtureArtifact("python312", "3.12.10", server.URL+"/312", data)
	b := fixtureArtifact("python313", "3.13.16", server.URL+"/313", data)
	provider, err := NewRuntimeProvider(
		root,
		RuntimeCatalog{SchemaVersion: "1.0", Artifacts: []RuntimeArtifact{a, b}},
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	locations := []ports.RuntimeLocation{}
	for _, version := range []string{"3.12.10", "3.13.16"} {
		plan, err := provider.Plan(
			context.Background(),
			ports.RuntimeRequest{
				Requirement: project.Requirement{
					Component:    "python",
					Semantics:    "pep440",
					Range:        "==" + version,
					Architecture: "amd64",
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		location, err := provider.Install(context.Background(), plan)
		if err != nil {
			t.Fatal(err)
		}
		locations = append(locations, location)
	}
	if locations[0].Root == locations[1].Root || locations[0].ABI == locations[1].ABI {
		t.Fatal("Python ABIs merged")
	}
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, ".deps"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := CheckSourcePreparation(source, false); err == nil || err.Error() != "SOURCE_COPY_REQUIRED" {
		t.Fatalf("hardcoded environment accepted in original tree: %v", err)
	}
}

func TestRuntimeArchiveRejectsTraversalAndTamperedPlans(t *testing.T) {
	root := runtimeWorkspace(t)
	data := runtimeZip(t, map[string]string{"runtime/python.exe": "synthetic", "runtime/../../escape.txt": "escape"})
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }),
	)
	defer server.Close()
	artifact := fixtureArtifact("python-fixture", "3.12.10", server.URL, data)
	provider, err := NewRuntimeProvider(
		root,
		RuntimeCatalog{SchemaVersion: "1.0", Artifacts: []RuntimeArtifact{artifact}},
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := provider.Plan(
		context.Background(),
		ports.RuntimeRequest{
			Requirement: project.Requirement{
				Component:    "python",
				Semantics:    "pep440",
				Range:        "==3.12.10",
				Architecture: "amd64",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	tampered := plan
	tampered.Destination = t.TempDir()
	if _, err := provider.Install(context.Background(), tampered); err == nil || err.Error() != "RUNTIME_PLAN_STALE" {
		t.Fatalf("tampered target accepted: %v", err)
	}
	if _, err := provider.Install(context.Background(), plan); err == nil {
		t.Fatal("archive traversal accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("archive escaped runtime root")
	}
}

func TestRuntimeReuseDetectsSameSizeExecutableTampering(t *testing.T) {
	root := runtimeWorkspace(t)
	data := runtimeZip(t, map[string]string{"runtime/python.exe": "trusted"})
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }),
	)
	defer server.Close()
	artifact := fixtureArtifact("python-fixture", "3.12.10", server.URL, data)
	provider, err := NewRuntimeProvider(
		root,
		RuntimeCatalog{SchemaVersion: "1.0", Artifacts: []RuntimeArtifact{artifact}},
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	req := ports.RuntimeRequest{
		Requirement: project.Requirement{
			Component:    "python",
			Semantics:    "pep440",
			Range:        "==3.12.10",
			Architecture: "amd64",
		},
	}
	plan, err := provider.Plan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	location, err := provider.Install(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(location.Executable, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	newPlan, err := provider.Plan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if newPlan.AlreadyInstalled {
		t.Fatal("same-size tampered runtime was reused")
	}
	if _, err := provider.Install(context.Background(), newPlan); err == nil {
		t.Fatal("corrupt installed environment silently adopted")
	}
}

func TestRuntimeSelectionHonorsSourceMinimumBeforePreferredVersion(t *testing.T) {
	root := runtimeWorkspace(t)
	data := runtimeZip(t, map[string]string{"runtime/python.exe": "synthetic"})
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }),
	)
	defer server.Close()
	a := fixtureArtifact("python312", "3.12.10", server.URL, data)
	b := fixtureArtifact("python313", "3.13.16", server.URL, data)
	provider, err := NewRuntimeProvider(
		root,
		RuntimeCatalog{SchemaVersion: "1.0", Artifacts: []RuntimeArtifact{a, b}},
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := provider.Plan(
		context.Background(),
		ports.RuntimeRequest{
			Requirement: project.Requirement{
				Component:        "python",
				Semantics:        "pep440",
				Range:            ">=3.10",
				PreferredVersion: "3.12.10",
				Architecture:     "amd64",
			},
			Constraints: []ports.MetadataConstraint{
				{Component: "python", Semantics: "pep440", Range: ">=3.13", Source: "pyproject.toml"},
			},
		},
	)
	if err != nil || plan.Version != "3.13.16" {
		t.Fatalf("preferred version violated source metadata: %+v %v", plan, err)
	}
}
