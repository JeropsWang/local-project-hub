package application_test

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

func TestProjectCoverReadsOnlyCommittedRaster(t *testing.T) {
	ctx := context.Background()
	_, projects := services(t)
	const id = "11111111-1111-4111-8111-111111111111"
	const path = "files/cover.png"
	const fixture = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jB1UAAAAASUVORK5CYII="
	cover, err := base64.StdEncoding.DecodeString(fixture)
	if err != nil {
		t.Fatal(err)
	}
	doc := declaration(t, id)
	doc.Appearance = &project.Appearance{CoverFileID: "cover"}
	doc.Files["cover"] = project.File{Availability: "included", BundlePath: path, Role: "cover"}
	if _, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc, IncludedFiles: map[string][]byte{path: cover}}); err != nil {
		t.Fatal(err)
	}
	got, err := projects.Cover(ctx, id)
	if err != nil || got != "data:image/png;base64,"+fixture {
		t.Fatalf("cover read %q: %v", got, err)
	}
	if _, err := projects.Save(ctx, application.SaveProjectRequest{
		RegisterProjectRequest: application.RegisterProjectRequest{Document: doc, IncludedFiles: map[string][]byte{path: []byte("<svg></svg>")}},
		ExpectedRevision:       1,
	}); err != nil {
		t.Fatal(err)
	}
	got, err = projects.Cover(ctx, id)
	if err != nil || got != "" {
		t.Fatalf("unsupported content reached preview: %q, %v", got, err)
	}
}
