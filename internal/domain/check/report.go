package check

import "time"

// Report keeps metadata readiness separate from execution and desktop verification.
type Report struct {
	ID                string    `json:"id"`
	ProjectID         string    `json:"projectId"`
	ProfileID         string    `json:"profileId"`
	ExpectedRevision  int32     `json:"expectedRevision"`
	SourceFingerprint string    `json:"sourceFingerprint"`
	Items             []Item    `json:"items"`
	Summary           string    `json:"summary"`
	CreatedAt         time.Time `json:"createdAt"`
}
