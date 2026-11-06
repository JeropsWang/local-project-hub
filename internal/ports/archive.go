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

// RevisionFileReader reads a bounded, verified file from a committed revision.
type RevisionFileReader interface {
	ReadRevisionFile(context.Context, project.ID, int32, string, int64) ([]byte, error)
}

type ArchiveWriter interface {
	Inspect(bundle.ExportInput) (bundle.Closure, error)
	Write(context.Context, bundle.ExportInput) (bundle.ExportResult, error)
}

type ArchiveReader interface {
	ReadArchive(context.Context, string) (bundle.ImportPayload, error)
}

type ImportPreviewRepository interface {
	SaveImportPreview(context.Context, string, bundle.ImportPlan) error
	LoadImportPreview(context.Context, string) (bundle.ImportPlan, error)
}
