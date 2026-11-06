package archive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

func TestExportPreservesBytesAndDoesNotReplaceDestination(t *testing.T) {
	project, err := os.ReadFile("../../../docs/contracts/examples/demo-bundle/project.json")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "example.zip")
	input := bundle.ExportInput{Destination: target, Project: project}
	written, err := (Writer{}).Write(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(written.Manifest.Files) != 1 || written.Manifest.Files[0].SHA256 != bundle.Digest(project) {
		t.Fatal("manifest does not describe original bytes")
	}
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (Writer{}).Write(context.Background(), input); err == nil {
		t.Fatal("replaced existing package")
	}
	after, err := os.ReadFile(target)
	if err != nil || bundle.Digest(before) != bundle.Digest(after) {
		t.Fatal("existing package changed", err)
	}
}

func TestComposeClosureRequiresNestedInputs(t *testing.T) {
	doc := project.Document{
		Files: map[string]project.File{"compose": {Availability: "included", BundlePath: "files/compose.yaml"}},
		Units: map[string]project.Unit{"database": {ComposeFileIDs: []string{"compose"}}},
	}
	files := map[string][]byte{
		"files/compose.yaml":     []byte("include: [./nested/base.yaml]\n"),
		"files/nested/base.yaml": []byte("configs:\n  setup:\n    file: ./setup.conf\n"),
	}
	if err := validateComposeClosure(doc, files); err == nil || err.Error() != "BUNDLE_COMPOSE_REFERENCE_MISSING" {
		t.Fatal("missing nested config was accepted", err)
	}
	files["files/nested/setup.conf"] = []byte("fixture")
	if err := validateComposeClosure(doc, files); err != nil {
		t.Fatal(err)
	}
	files["files/nested/base.yaml"] = []byte("services:\n  db:\n    env_file: [service.env]\n")
	files["files/nested/service.env"] = []byte("FIXTURE=private")
	if err := validateComposeClosure(doc, files); err == nil ||
		err.Error() != "BUNDLE_COMPOSE_SECRET_EXTERNAL_REQUIRED" {
		t.Fatal("service environment file was treated as portable", err)
	}
}
