// Package ports defines host capabilities without importing concrete adapters.
package ports

import (
	"context"

	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/workspace"
)

// WorkspaceFactory opens one independently locked local workspace.
type WorkspaceFactory interface {
	Initialize(context.Context, string, string) (Repository, error)
	Open(context.Context, string) (Repository, error)
}

// Repository owns its database and workspace lock until Close.
type Repository interface {
	Info() workspace.View
	Close() error
	HasActive(context.Context) (bool, error)
	ConfirmRelocation(context.Context, string) (workspace.View, error)
	Register(context.Context, project.Document, []byte, []project.Binding, map[string][]byte) (project.Snapshot, error)
	Save(context.Context, project.Document, int32, []byte, []project.Binding, map[string][]byte) (project.Snapshot, error)
	Get(context.Context, string) (project.Snapshot, error)
	List(context.Context, project.Query) ([]project.Snapshot, error)
	Binding(context.Context, string, string) (project.Binding, error)
}
