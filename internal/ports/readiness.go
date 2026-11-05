package ports

import (
	"context"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

// ReadinessAdapter validates support before any effect, then waits on read-only
// observations. Availability evidence never substitutes for stop certification.
type ReadinessAdapter interface {
	ValidateReadiness(run.UnitPlan) error
	WaitReady(context.Context, run.UnitPlan, run.Identity) error
}

// ProcessStatus queries the authenticated control endpoint without saving/stopping.
type ProcessStatus interface {
	ControlStatus(context.Context, run.Identity) (string, error)
}

// ProcessLogs separates bounded log viewing from process-control authority.
type ProcessLogs interface {
	ReadLog(context.Context, run.Identity, string, int) (run.LogPage, error)
}
