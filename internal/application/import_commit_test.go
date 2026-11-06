package application_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/adapters/archive"
	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func TestImportRejectsReplacedPackageAndPreservesExistingProject(t *testing.T) {
	ctx := context.Background()
	ws, projects := services(t)
	id := project.ID("11111111-1111-4111-8111-111111111111")
	doc := declaration(t, string(id))
	originalName := doc.Name
	folder := t.TempDir()
	source := filepath.Join(folder, "source.zip")
	writer := archive.Writer{}
	write := func(name string, doc project.Document) {
		t.Helper()
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = writer.Write(ctx, bundle.ExportInput{Destination: name, Project: data}); err != nil {
			t.Fatal(err)
		}
	}
	write(source, doc)
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewBundleService(ws, writer, archive.Reader{})
	preview, err := service.PreviewImport(ctx, application.ImportPreviewRequest{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = projects.Get(ctx, id); err == nil || err.Error() != "PROJECT_MISSING" {
		t.Fatal("preview registered a project", err)
	}
	request := application.CommitImportRequest{PreviewID: preview.ID}
	plan, err := service.PlanImport(ctx, request)
	if err != nil || len(plan.RequiredBindings) == 0 {
		t.Fatal("expected pending bindings", err)
	}
	tasks := application.NewTaskService(
		ws,
		application.NewPlanService(ws, nil),
		map[task.Kind]ports.TaskExecutor{task.Import: service},
	)
	defer tasks.Close(ctx)
	doc.Name = "replacement"
	replacement := filepath.Join(folder, "replacement.zip")
	write(replacement, doc)
	other, err := os.ReadFile(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source, other, 0600); err != nil {
		t.Fatal(err)
	}
	submit := func(key string) application.TaskView {
		t.Helper()
		view, err := tasks.Submit(
			ctx,
			application.SubmitPlanRequest{PlanID: plan.ID, Fingerprint: plan.Fingerprint, RequestID: key},
		)
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	failed := waitTask(t, tasks, submit("replaced").ID, task.Failed)
	if failed.Error == nil || failed.Error.Code != "BUNDLE_CHANGED" {
		t.Fatal("replaced bundle was not rejected", failed.Error)
	}
	if _, err = projects.Get(ctx, id); err == nil || err.Error() != "PROJECT_MISSING" {
		t.Fatal("failed import published a project", err)
	}
	if err = os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	waitTask(t, tasks, submit("original").ID, task.Succeeded)
	saved, err := projects.Get(ctx, id)
	if err != nil || saved.Document.Name != originalName || saved.CheckSummary != "unverified" ||
		saved.RunSummary != "stopped" {
		t.Fatal("import did not preserve configuration without claiming readiness", err)
	}
	if _, err = service.PlanImport(ctx, request); err == nil || err.Error() != "PROJECT_EXISTS" {
		t.Fatal("duplicate import accepted", err)
	}
}
