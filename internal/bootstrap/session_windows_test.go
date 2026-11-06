package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/desktop/services"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
)

// Exercise the same service used by Wails with an isolated, empty workspace.
func TestDesktopWorkspaceSetupAndSafeExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := services.NewManager(newSession)
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SelectWorkspace(ctx, application.InitWorkspaceRequest{Root: source}, false); err == nil {
		t.Fatal("source folder opened as workspace")
	}
	workspace := filepath.Join(root, "workspace")
	view, err := manager.SelectWorkspace(ctx, application.InitWorkspaceRequest{Root: workspace, Name: "测试工作空间"}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.ServiceShutdown(); err != nil {
			t.Error(err)
		}
	})
	if view.Root != workspace || view.State != "ready" {
		t.Fatalf("unexpected workspace: %+v", view)
	}
	if projects, err := manager.Projects(ctx); err != nil || len(projects) != 0 {
		t.Fatalf("unexpected project list: %v, %v", projects, err)
	}
	exit, err := manager.RequestExit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		view, err := manager.Task(ctx, exit.ID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Terminal(view.State) {
			if allowed, err := manager.CanQuit(ctx); view.State != task.Succeeded || err != nil || !allowed {
				t.Fatalf("safe exit did not complete: %+v, %t, %v", view, allowed, err)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}
