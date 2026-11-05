package ports

import (
	"context"

	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

// InspectionRequest scopes file reads to declared bindings and one manager revision.
type InspectionRequest struct {
	Document      project.Document
	Bindings      map[string]project.Binding
	WorkspaceRoot string
	Revision      int32
	UnitIDs       []string
}

// SourceInspection fingerprints declared inputs without certifying a clean working tree.
type SourceInspection struct {
	Fingerprint string
	Items       []check.Item
	Constraints []MetadataConstraint
}

// MetadataConstraint is observed from a bounded source manifest, never executed.
type MetadataConstraint struct {
	Component string
	Semantics string
	Range     string
	Source    string
}

// FileInspector reads bounded declared files and never invokes a project script.
type FileInspector interface {
	Inspect(context.Context, InspectionRequest) (SourceInspection, error)
}
