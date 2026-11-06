package environment

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestInspectSourceReadsOnlyRecognizedRootMarkers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sample")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"package.json", "compose.yaml", ".env", "nested/go.mod"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("not parsed"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "pom.xml"), 0700); err != nil {
		t.Fatal(err)
	}
	result, err := InspectSource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "sample" || !slices.Equal(result.Markers, []string{"package.json", "compose.yaml"}) {
		t.Fatalf("unexpected inspection: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, ".workspace.lock")); !os.IsNotExist(err) {
		t.Fatalf("inspection wrote a lock: %v", err)
	}
}
