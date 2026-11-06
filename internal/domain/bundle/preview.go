package bundle

import (
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

// Inspection contains portable evidence only. Local bindings and permissions
// are resolved separately when the user registers the project.
type Inspection struct {
	ID               string           `json:"id,omitempty"`
	ExpiresAt        time.Time        `json:"expiresAt,omitempty"`
	Identity         string           `json:"bundleIdentity"`
	Document         project.Document `json:"document"`
	RequiredBindings []string         `json:"requiredBindings"`
	Warnings         []string         `json:"warnings"`
}

// ImportPlan pins the source identity and target choices. No archive bytes or
// source-host bindings are persisted with the task.
type ImportPlan struct {
	SourcePath string
	Identity   string
	ProjectID  string
	ExpiresAt  time.Time
	Bindings   []project.Binding
}

type ImportPayload struct {
	Inspection Inspection
	Project    []byte
	Files      map[string][]byte
}
