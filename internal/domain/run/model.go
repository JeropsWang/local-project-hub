// Package run models acquired resources and immutable runtime identity.
package run

import (
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"time"
)

// Identity must be rechecked before controlling a process or container.
type Identity struct {
	Kind         string `json:"kind"`
	PID          uint32 `json:"pid,omitempty"`
	CreationTime string `json:"creationTime,omitempty"`
	Executable   string `json:"executable,omitempty"`
	ControlID    string `json:"controlId,omitempty"`
	Engine       string `json:"engine,omitempty"`
	ContainerID  string `json:"containerId,omitempty"`
}

// Snapshot fixes the commands and scripts used by the current run, even after edits.
type Snapshot struct {
	ID           string            `json:"id"`
	ProfileRef   project.Ref       `json:"profileRef"`
	Document     project.Document  `json:"document"`
	ScriptBytes  map[string][]byte `json:"scriptBytes"`
	InputHashes  map[string]string `json:"inputHashes"`
	Acquisitions []Acquisition     `json:"acquisitions"`
	CreatedAt    time.Time         `json:"createdAt"`
}

// Acquisition distinguishes newly controlled resources from preexisting references.
type Acquisition struct {
	ResourceID    string   `json:"resourceId"`
	UnitID        string   `json:"unitId"`
	Identity      Identity `json:"identity"`
	NewlyAcquired bool     `json:"newlyAcquired"`
	ControlGrant  string   `json:"controlGrant,omitempty"`
}
