package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
)

func TestSourceZIPReportsMissingBundleManifest(t *testing.T) {
	var raw bytes.Buffer
	writer := zip.NewWriter(&raw)
	for _, name := range []string{"example-main/", "example-main/README.md"} {
		if _, err := writer.Create(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err := validateZIP(context.Background(), bytes.NewReader(raw.Bytes()), int64(raw.Len()))
	if err == nil || err.Error() != "BUNDLE_MANIFEST_MISSING" {
		t.Fatalf("source ZIP should report a missing manifest: %v", err)
	}
}

func TestRejectWindowsEscapeAndAliases(t *testing.T) {
	for _, names := range [][]string{
		{"../outside"}, {"files/data:stream"}, {"files/CON.txt"},
		{"files/a.txt", "files/A.txt"}, {"files/e\u0301.txt"},
	} {
		var raw bytes.Buffer
		writer := zip.NewWriter(&raw)
		if _, err := writer.Create(bundle.ManifestFile); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			entry, err := writer.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = entry.Write([]byte("fixture")); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		_, _, err := validateZIP(context.Background(), bytes.NewReader(raw.Bytes()), int64(raw.Len()))
		if err == nil || err.Error() != "BUNDLE_PATH_INVALID" && err.Error() != "BUNDLE_ZIP_ENTRY_INVALID" && err.Error() != "BUNDLE_ZIP_INVALID" {
			t.Fatalf("unsafe entry reached manifest validation: %v %v", names, err)
		}
	}
}
