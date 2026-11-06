package ports

import (
	"context"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

// ResourceRepository owns local grants and atomic reference/stop journals.
type ResourceRepository interface {
	Resource(context.Context, string) (resource.Record, error)
	ListResources(context.Context) ([]resource.Record, error)
	SaveResource(context.Context, resource.Record, string) (resource.Record, error)
	AcquireResource(context.Context, string, string, resource.Use) error
	ReleaseResource(context.Context, string, resource.Use) error
	MarkResourceStopped(context.Context, string, string) error
	ClearSettledStart(context.Context, string, string, string) error
}

// ComposeAuthorization validates backend-authored source bindings before a CLI effect
// and publishes exact observed identities even when Compose only partly succeeds.
type ComposeAuthorization interface {
	AuthorizeCompose(context.Context, ComposeSpec) error
	BeginCompose(context.Context, ComposeSpec, string) error
	RecordComposeInstances(context.Context, ComposeSpec, string, []run.Identity, bool) error
}
