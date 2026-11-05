package ports

import (
	"context"

	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

// ComposeFile preserves application order and binds a local input to its reviewed hash.
type ComposeFile struct {
	Path   string
	SHA256 string
}

// ComposeSpec is backend-authored. A nonempty existing project name needs a local grant.
type ComposeSpec struct {
	ResourceID        string
	Engine            string
	ExpectedEngineID  string
	ProjectName       string
	ControlGrant      string
	WorkingDir        string
	Files             []ComposeFile
	Services          []string
	ConfigFingerprint string
}

// ComposePreview contains a non-secret fingerprint and normalized selected services.
type ComposePreview struct {
	Spec        ComposeSpec
	ProjectName string
	Fingerprint string
	Effects     []string
}

// ComposePlanner inspects config without running hooks, expanding secrets or starting services.
type ComposePlanner interface {
	PreviewCompose(context.Context, ComposeSpec) (ComposePreview, error)
}

// ComposeInvocation is built by the Docker adapter; it is never accepted from frontend JSON.
type ComposeInvocation struct {
	Pipe      string
	Arguments []string
	CWD       string
}

// ComposeRunner executes only the backend's fixed config/up argument builders.
type ComposeRunner interface {
	RunCompose(context.Context, ComposeInvocation) ([]byte, error)
}

// ContainerObservation contains inventory metadata, never the container's environment values.
type ContainerObservation struct {
	Identity           run.Identity `json:"identity"`
	Name               string       `json:"name"`
	Image              string       `json:"image"`
	Status             string       `json:"status"`
	Running            bool         `json:"running"`
	Health             string       `json:"health"`
	ComposeProject     string       `json:"composeProject"`
	ComposeService     string       `json:"composeService"`
	ComposeFiles       string       `json:"composeFiles"`
	WorkingDir         string       `json:"workingDir"`
	ConfigHash         string       `json:"configHash"`
	ManagerGrantID     string       `json:"managerGrantId,omitempty"`
	ManagerResourceID  string       `json:"managerResourceId,omitempty"`
	ManagerOperationID string       `json:"managerOperationId,omitempty"`
}

// ContainerStopResult keeps uncertainty visible. Normal signal delivery alone is not completion.
type ContainerStopResult struct {
	Graceful       bool   `json:"graceful"`
	Code           string `json:"code"`
	Signal         string `json:"signal,omitempty"`
	AlreadyStopped bool   `json:"alreadyStopped"`
}

// ContainerAuthorization requires a separate local grant and journals each stop
// signal attempt before delivery. Repeated waits must not send a second signal.
type ContainerAuthorization interface {
	AuthorizeContainer(context.Context, run.Identity) error
	BeginContainerStop(context.Context, run.Identity, string) (bool, error)
}

// ContainerAdapter never infers control authority from a name, label or resource UUID.
type ContainerAdapter interface {
	Inventory(context.Context, string) ([]ContainerObservation, error)
	Start(context.Context, ComposeSpec) ([]run.Identity, error)
	Stop(context.Context, run.Identity) (ContainerStopResult, error)
}
