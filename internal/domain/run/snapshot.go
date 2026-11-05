package run

import "github.com/JeropsWang/local-project-hub/internal/domain/project"

// Command freezes resolved paths/argv and secret references, never secret values.
type Command struct {
	Executable  string            `json:"executable"`
	Args        []string          `json:"args"`
	CWD         string            `json:"cwd"`
	Environment map[string]string `json:"environment"`
	SecretRefs  map[string]string `json:"secretRefs"`
}

// UnitPlan is backend-compiled before the first startup effect. Resource authority
// is pinned separately and must still be revalidated immediately before use.
type UnitPlan struct {
	Readiness           *project.Probe `json:"readiness,omitempty"`
	ID                  string         `json:"id"`
	InstanceID          string         `json:"instanceId,omitempty"`
	Kind                string         `json:"kind"`
	Control             string         `json:"control"`
	ResourceID          string         `json:"resourceId"`
	ResourceFingerprint string         `json:"resourceFingerprint,omitempty"`
	Start               *Command       `json:"start,omitempty"`
	Stop                *Command       `json:"stop,omitempty"`
	StopKind            string         `json:"stopKind,omitempty"`
	TimeoutSeconds      int            `json:"timeoutSeconds"`
}

// Record separates immutable launch intent from journaled acquisition progress.
// A starting/stopping record survives an uncertain external effect; it is never
// replayed on recovery merely because the database contains a PID or an intent.
type Record struct {
	Snapshot     Snapshot      `json:"snapshot"`
	Version      int64         `json:"version"`
	State        string        `json:"state"`
	FailureCode  string        `json:"failureCode,omitempty"`
	Acquisitions []Acquisition `json:"acquisitions"`
}
