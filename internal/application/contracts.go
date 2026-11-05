// Package application coordinates core use cases through injected ports.
package application

import (
	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/domain/workspace"
)

// Stable ID types prevent accidental cross-kind identity comparisons.
type (
	ProjectID   = project.ID
	WorkspaceID string
	ProfileID   = project.ProfileID
	TaskID      = task.ID
	PlanID      = task.PlanID
	RunID       string
	PreviewID   string
	Revision    = int32
	EventCursor string
)

// Shared DTO aliases retain one authoritative domain representation.
type (
	WorkspaceView      = workspace.View
	ProjectView        = project.Snapshot
	ProjectQuery       = project.Query
	CheckItem          = check.Item
	TaskView           = task.View
	TaskKind           = task.Kind
	TaskState          = task.State
	OperationPlan      = task.Plan
	SubmitPlanRequest  = task.SubmitRequest
	ResolveTaskRequest = task.ResolveRequest
	Event              = task.Event
)

// InitWorkspaceRequest explicitly selects where managed files will live.
type InitWorkspaceRequest struct {
	Root string `json:"root"`
	Name string `json:"name"`
}

// RegisterProjectRequest separates portable declarations from local bindings.
type RegisterProjectRequest struct {
	Document      project.Document  `json:"document"`
	Bindings      []project.Binding `json:"bindings"`
	IncludedFiles map[string][]byte `json:"includedFiles"`
}

// SaveProjectRequest uses optimistic concurrency; absent files reuse prior bytes.
type SaveProjectRequest struct {
	RegisterProjectRequest
	ExpectedRevision Revision `json:"expectedRevision"`
}

// ProfileRef fixes the configuration revision used by a later operation.
type ProfileRef = project.Ref
