package project

// Executable identifies a runtime, binding or a fixed supported system tool.
// Schema validation discriminates its shape before it reaches an adapter.
type Executable struct {
	Kind          string `json:"kind"`
	RequirementID string `json:"requirementId,omitempty"`
	RelativePath  string `json:"relativePath,omitempty"`
	Binding       string `json:"binding,omitempty"`
	Tool          string `json:"tool,omitempty"`
}

// Command keeps arguments separate and resolves secrets only as environment bindings.
type Command struct {
	Executable     Executable        `json:"executable"`
	Args           []string          `json:"args"`
	CWD            Path              `json:"cwd"`
	EnvBindings    map[string]string `json:"envBindings"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
}
