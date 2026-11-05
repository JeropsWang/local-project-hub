package ports

import (
	"context"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/check"
)

// ProbeRequest is constructed by the application, never accepted as a frontend command.
type ProbeRequest struct {
	Tool       string
	Location   string
	Executable string
	Args       []string
	CWD        string
	Timeout    time.Duration
}

// Observation is a bounded read-only result; it is not a project runtime test.
type Observation struct {
	Component    string
	Locator      string
	Version      string
	Architecture string
	ABI          string
	Capabilities []string
	Status       check.Status
	ReasonCode   string
	ExitCode     int
	OutputBytes  int
	Evidence     check.Evidence
}

// Probe exposes only fixed, read-only tool discovery.
type Probe interface {
	Discover(context.Context, ProbeRequest) (Observation, error)
}
