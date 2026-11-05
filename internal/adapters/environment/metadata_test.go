package environment

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func TestInspectorDoesNotTrustUnverifiedSecretConfigHash(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, ".env")
	if err := os.WriteFile(file, []byte("DEMO_VALUE=synthetic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	request := ports.InspectionRequest{Document: project.Document{Files: map[string]project.File{"settings": {Availability: "external", Path: &project.Path{Binding: "source", RelativePath: ".env"}, Role: "config", Required: true, ExpectedSHA256: strings.Repeat("0", 64)}}}, Bindings: map[string]project.Binding{"source": {Key: "source", Kind: "path", LocalLocator: root}}}
	result, err := (StaticInspector{}).Inspect(context.Background(), request)
	if err != nil || len(result.Items) != 1 || result.Items[0].Status != check.Unknown {
		t.Fatalf("unchecked secret hash reported satisfied: %+v %v", result, err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "DEMO_VALUE=synthetic\n" {
		t.Fatal("detection changed source")
	}
}

func TestInspectorReportsDeclaredFileMismatchWithoutRunningIt(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "entry.mjs")
	if err := os.WriteFile(script, []byte("throw new Error('must never execute');\n"), 0600); err != nil {
		t.Fatal(err)
	}
	request := ports.InspectionRequest{Document: project.Document{Files: map[string]project.File{"entry": {Availability: "external", Path: &project.Path{Binding: "source", RelativePath: "entry.mjs"}, Role: "source", Required: true, ExpectedSHA256: strings.Repeat("0", 64)}}}, Bindings: map[string]project.Binding{"source": {Key: "source", Kind: "path", LocalLocator: root}}}
	result, err := (StaticInspector{}).Inspect(context.Background(), request)
	if err != nil || result.Items[0].Status != check.Incompatible || result.Items[0].Reasons[0].Code != "FILE_HASH_MISMATCH" {
		t.Fatalf("mismatch: %+v %v", result, err)
	}
}

func TestMetadataFingerprintIsStableAndTracksManifestChanges(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{"package.json": `{"engines":{"node":">=22.19.0"}}`, "package-lock.json": `{"lockfileVersion":3,"packages":{"":{"engines":{"node":">=22.19.0"}}}}`} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	req := ports.InspectionRequest{Document: project.Document{Requirements: map[string]project.Requirement{"node": {Component: "node"}}}, Bindings: map[string]project.Binding{"source": {Kind: "path", LocalLocator: root}}}
	first, err := (StaticInspector{}).Inspect(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for range 24 {
		next, err := (StaticInspector{}).Inspect(context.Background(), req)
		if err != nil || next.Fingerprint != first.Fingerprint {
			t.Fatal("unchanged manifests produced a stale-plan fingerprint")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"engines":{"node":">=24.0.0"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := (StaticInspector{}).Inspect(context.Background(), req)
	if err != nil || changed.Fingerprint == first.Fingerprint {
		t.Fatal("manifest change not detected")
	}
}
