package ports

import (
	"context"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

// RuntimeRequest selects a compatible artifact from the manager's trusted catalog.
type RuntimeRequest struct {
	Requirement project.Requirement
	Constraints []MetadataConstraint
}

// RuntimeInstallPlan contains no client-selected URL; its destination is verified on use.
type RuntimeInstallPlan struct {
	ArtifactID       string `json:"artifactId"`
	Component        string `json:"component"`
	Version          string `json:"version"`
	Architecture     string `json:"architecture"`
	Destination      string `json:"destination"`
	Fingerprint      string `json:"fingerprint"`
	AlreadyInstalled bool   `json:"alreadyInstalled"`
}

// RuntimeLocation is a local result, never portable runtime certification.
type RuntimeLocation struct {
	Root         string `json:"root"`
	Executable   string `json:"executable"`
	Component    string `json:"component"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	ABI          string `json:"abi,omitempty"`
	Fingerprint  string `json:"fingerprint"`
}

// RuntimeProvider prepares supported SDKs without changing system PATH or registry.
type RuntimeProvider interface {
	Plan(context.Context, RuntimeRequest) (RuntimeInstallPlan, error)
	Install(context.Context, RuntimeInstallPlan) (RuntimeLocation, error)
}

// EnvironmentRepository records local runtime choices without editing portable declarations.
type EnvironmentRepository interface {
	BindRuntime(context.Context, string, string, RuntimeLocation) error
}
