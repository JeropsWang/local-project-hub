package application_test

import (
	"archive/zip"
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/adapters/archive"
	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func TestExportUsesFixedRevisionAfterNewSave(t *testing.T) {
	ctx := context.Background()
	ws, projects := services(t)
	doc := declaration(t, "11111111-1111-4111-8111-111111111111")
	doc.Files["local-script"] = project.File{
		Availability: "included",
		BundlePath:   "files/start.ps1",
		Required:     true,
		Role:         "script",
	}
	req := application.RegisterProjectRequest{
		Document:      doc,
		IncludedFiles: map[string][]byte{"files/start.ps1": []byte("Write-Output 'fixture'")},
	}
	if _, err := projects.Register(ctx, req); err != nil {
		t.Fatal(err)
	}
	ref := project.Ref{ProjectID: project.ID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 1}
	bundles := application.NewBundleService(ws, archive.Writer{}, archive.Reader{})
	target := filepath.Join(t.TempDir(), "original.zip")
	plan, err := bundles.PlanExport(ctx, application.ExportRequest{ProfileRef: ref, DestinationPath: target})
	if err != nil {
		t.Fatal(err)
	}
	doc.Name = "new revision"
	req.Document = doc
	if _, err = projects.Save(ctx, application.SaveProjectRequest{RegisterProjectRequest: req, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	tasks := application.NewTaskService(
		ws,
		application.NewPlanService(ws, nil),
		map[task.Kind]ports.TaskExecutor{task.Export: bundles},
	)
	defer tasks.Close(ctx)
	view, err := tasks.Submit(
		ctx,
		application.SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: "export-fixture"},
	)
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, tasks, view.ID, task.Succeeded)
	preview, err := bundles.PreviewImport(ctx, application.ImportPreviewRequest{SourcePath: target})
	if err != nil || preview.Document.Name == "new revision" || len(preview.RequiredBindings) == 0 {
		t.Fatal("preview did not preserve configuration and list target bindings", err)
	}
	reader, err := zip.OpenReader(target)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	file, err := reader.Open("project.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := contract.DecodeProject(data)
	if err != nil || saved.Name == "new revision" {
		t.Fatal("export read latest instead of pinned revision", err)
	}
}
