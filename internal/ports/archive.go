package ports

import (
	"context"

	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

// RevisionArchiveReader loads only committed portable bytes and checks them
// against the stored revision inventory.
type RevisionArchiveReader interface {
	ExportRevision(context.Context, project.Ref) (bundle.ExportInput, error)
}

type ArchiveWriter interface {
	Inspect(bundle.ExportInput) (bundle.Closure, error)
	Write(context.Context, bundle.ExportInput) (bundle.ExportResult, error)
}

type ArchiveReader interface {
	InspectArchive(context.Context, string) (bundle.Inspection, error)
}
