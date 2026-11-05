package ports

import (
	"context"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

// SecretResolver resolves a local credential reference only in execution memory.
type SecretResolver interface {
	ResolveSecret(context.Context, string) (string, error)
}

// UnitRunner executes one-shot work. The caller controls whether cancellation is
// allowed during that effect; wait-policy work must converge before rollback.
type UnitRunner interface {
	RunUnit(context.Context, UnitExecution) error
}

// UnitExecution keeps effect policy and immutable scripts together with argv.
type UnitExecution struct {
	Process      ProcessSpec
	CancelPolicy string
}

// RunCompiler resolves one immutable revision and reads reviewed script bytes.
// It performs no project execution and never embeds secret values in its result.
type RunCompiler interface {
	CompileRun(context.Context, InspectionRequest) (run.Snapshot, error)
}
