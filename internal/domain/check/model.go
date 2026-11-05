// Package check models evidence separately from execution state.
package check

import "time"

// Status describes metadata compatibility, not successful application execution.
type Status string

const (
	Satisfied       Status = "satisfied"
	Missing         Status = "missing"
	Incompatible    Status = "incompatible"
	BindingRequired Status = "binding-required"
	Unknown         Status = "unknown"
)

// Item is an individual requirement result with actionable evidence and repairs.
type Item struct {
	RequirementID string       `json:"requirementId"`
	Status        Status       `json:"status"`
	Evidence      []Evidence   `json:"evidence"`
	Reasons       []Reason     `json:"reasons"`
	Repairs       []RepairStep `json:"repairs"`
}

// Evidence records what was observed and when without elevating declarations.
type Evidence struct {
	Kind       string    `json:"kind"`
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observedAt"`
	Version    string    `json:"version,omitempty"`
}

// Reason uses a stable code and safe display information.
type Reason struct {
	Code    string `json:"code"`
	Subject string `json:"subject"`
	Message string `json:"message"`
}

// RepairStep explains possible effects without executing them.
type RepairStep struct {
	ID                 string `json:"id"`
	Label              string `json:"label"`
	Effect             string `json:"effect"`
	AutomaticSupported bool   `json:"automaticSupported"`
}
