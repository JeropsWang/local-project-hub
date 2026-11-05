package ports

import (
	"context"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/preparation"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

// ProjectPreparationRequest is backend-authored from the saved selected profile.
type ProjectPreparationRequest struct {
	Ref          project.Ref
	Document     project.Document
	UnitIDs      []string
	Bindings     map[string]project.Binding
	RuntimePlans map[string]RuntimeInstallPlan
}

// ProjectPreparer copies source and restores dependencies only into owned disposable directories.
type ProjectPreparer interface {
	Plan(context.Context, ProjectPreparationRequest) (preparation.Plan, error)
	Prepare(context.Context, preparation.Plan, map[string]RuntimeLocation) (preparation.Receipt, error)
}

// PreparationCommand is scoped to one new environment; it must not inherit user credentials.
type PreparationCommand struct {
	Executable  string
	Args        []string
	CWD         string
	OwnedRoot   string
	Environment map[string]string
	Timeout     time.Duration
}

// PreparationResult exposes controlled metadata; raw package output stays in a local bounded log.
type PreparationResult struct {
	ExitCode int
	LogFile  string
}

// PreparationRunner may terminate only the disposable process group it creates.
type PreparationRunner interface {
	Run(context.Context, PreparationCommand) (PreparationResult, error)
}

// PreparedEnvironmentRepository atomically selects a complete environment without editing a portable revision.
type PreparedEnvironmentRepository interface {
	ActivateEnvironment(context.Context, preparation.Receipt) error
}
