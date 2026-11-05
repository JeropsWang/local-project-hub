package project

import "time"

// Binding is local-only. Secret values must never be provided as locators.
type Binding struct {
	Key          string `json:"key"`
	Kind         string `json:"kind"`
	LocalLocator string `json:"localLocator,omitempty"`
	SecretRef    string `json:"secretRef,omitempty"`
}

// Snapshot is a committed immutable configuration revision.
type Snapshot struct {
	Document     Document  `json:"document"`
	Revision     int32     `json:"revision"`
	ContentHash  string    `json:"contentHash"`
	CreatedAt    time.Time `json:"createdAt"`
	CheckSummary string    `json:"checkSummary"`
	RunSummary   string    `json:"runSummary"`
}

// Query applies local library filtering with bounded pagination.
type Query struct {
	Group  string `json:"group"`
	Search string `json:"search"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}
