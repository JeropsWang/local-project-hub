package check

import "time"

// Report keeps metadata readiness separate from execution and desktop verification.
type Report struct {
	ID                string       `json:"id"`
	ProjectID         string       `json:"projectId"`
	ProfileID         string       `json:"profileId"`
	ExpectedRevision  int32        `json:"expectedRevision"`
	SourceFingerprint string       `json:"sourceFingerprint"`
	Items             []Item       `json:"items"`
	Constraints       []Constraint `json:"constraints"`
	Summary           string       `json:"summary"`
	CreatedAt         time.Time    `json:"createdAt"`
}

// Constraint records a static source requirement used in metadata intersection.
type Constraint struct {
	Component string `json:"component"`
	Semantics string `json:"semantics"`
	Range     string `json:"range"`
	Source    string `json:"source"`
}
