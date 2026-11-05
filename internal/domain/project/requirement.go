package project

// Requirement keeps each ecosystem's version semantics and evidence provenance.
type Requirement struct {
	Component        string              `json:"component"`
	Location         string              `json:"location"`
	Semantics        string              `json:"semantics"`
	Range            string              `json:"range"`
	PreferredVersion string              `json:"preferredVersion,omitempty"`
	Required         bool                `json:"required"`
	Stages           []string            `json:"stages"`
	Architecture     string              `json:"architecture"`
	ABI              string              `json:"abi,omitempty"`
	Capabilities     []string            `json:"capabilities"`
	Evidence         RequirementEvidence `json:"evidence"`
}

// RequirementEvidence is an untrusted declaration until independently observed.
type RequirementEvidence struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
}
