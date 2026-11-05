package project

// Unit is a closed, schema-discriminated process/task/compose/external declaration.
// Inapplicable variant fields stay empty and are never interpreted by adapters.
type Unit struct {
	ResourceID     string   `json:"resourceId"`
	Label          string   `json:"label"`
	DependsOn      []string `json:"dependsOn"`
	RequirementIDs []string `json:"requirementIds"`
	FeatureID      string   `json:"featureId,omitempty"`
	Kind           string   `json:"kind"`
	Control        string   `json:"control"`
	Start          *Command `json:"start,omitempty"`
	Command        *Command `json:"command,omitempty"`
	Readiness      *Probe   `json:"readiness,omitempty"`
	Stop           *Stop    `json:"stop,omitempty"`
	Effect         string   `json:"effect,omitempty"`
	CancelPolicy   string   `json:"cancelPolicy,omitempty"`
	EngineBinding  string   `json:"engineBinding,omitempty"`
	ComposeFileIDs []string `json:"composeFileIds,omitempty"`
	Services       []string `json:"services,omitempty"`
	Binding        string   `json:"binding,omitempty"`
}

// Probe describes readiness; a successful probe alone is not lifecycle certification.
type Probe struct {
	Kind           string `json:"kind"`
	Host           string `json:"host,omitempty"`
	Port           int    `json:"port,omitempty"`
	AdapterID      string `json:"adapterId,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

// Stop describes graceful shutdown. Manual/none do not permit automatic management.
type Stop struct {
	Kind           string   `json:"kind"`
	Command        *Command `json:"command,omitempty"`
	AdapterID      string   `json:"adapterId,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
	Reason         string   `json:"reason,omitempty"`
}
