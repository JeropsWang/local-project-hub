// Package project models portable declarations independently of the host and desktop.
package project

// Document is the versioned declaration; machine paths, secrets and grants live elsewhere.
type Document struct {
	SchemaVersion string                 `json:"schemaVersion"`
	ProjectID     string                 `json:"projectId"`
	Name          string                 `json:"name"`
	Group         string                 `json:"group,omitempty"`
	Description   string                 `json:"description,omitempty"`
	Source        Source                 `json:"source"`
	Requirements  map[string]Requirement `json:"requirements"`
	Files         map[string]File        `json:"files"`
	Units         map[string]Unit        `json:"units"`
	Profiles      map[string]Profile     `json:"profiles"`
	Appearance    *Appearance            `json:"appearance,omitempty"`
}

// Source records identity, never a claim of runtime verification.
type Source struct {
	Kind     string `json:"kind"`
	Identity string `json:"identity"`
	RefLabel string `json:"refLabel,omitempty"`
}

// Profile selects entry units and explicitly enabled optional features.
type Profile struct {
	Label      string   `json:"label"`
	UnitIDs    []string `json:"unitIds"`
	FeatureIDs []string `json:"featureIds"`
}

// Appearance references portable artwork rather than a host absolute path.
type Appearance struct {
	CoverFileID string `json:"coverFileId,omitempty"`
}
