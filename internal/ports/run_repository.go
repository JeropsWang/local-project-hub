package ports

import (
	"context"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

// RunRepository journals ownership independently of the editable project revision.
type RunRepository interface {
	CreateRun(context.Context, run.Snapshot) error
	Run(context.Context, string) (run.Record, error)
	ActiveRuns(context.Context) ([]run.Record, error)
	UpdateRun(context.Context, run.Record, int64) error
}
