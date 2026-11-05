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
