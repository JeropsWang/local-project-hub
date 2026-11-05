package ports

import (
	"context"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
)

// TaskRepository is a separate capability; project-only repositories need not implement it.
type TaskRepository interface {
	SavePlan(context.Context, task.Plan) error
	LoadPlan(context.Context, task.PlanID) (task.Plan, error)
	FindTaskRequest(context.Context, string) (task.Record, error)
	CreateTask(context.Context, task.Record) (task.Record, error)
	Task(context.Context, task.ID) (task.Record, error)
	QueuedTasks(context.Context) ([]task.Record, error)
	TransitionTask(context.Context, task.ID, string, task.State, *task.AppError, task.EventData) (task.Record, error)
	AppendTaskEvent(context.Context, task.ID, task.EventData) (task.Record, error)
	TaskEvents(context.Context, task.ID, string) ([]task.Event, error)
	RecoverTasks(context.Context) (int, error)
	ReserveTaskResources(context.Context, task.ID, []string) (bool, error)
	ReleaseTaskResources(context.Context, task.ID) error
	SetTaskResumeAction(context.Context, task.ID, string) error
}

// TaskControl offers explicit safe points; cancelling an HTTP call does not cancel a job.
type TaskControl interface {
	Checkpoint(context.Context) error
	Progress(context.Context, task.EventData) error
	ResumeAction() string
}

// TaskExecutor implements a whole operation including its own rollback and safe waits.
type TaskExecutor interface {
	Execute(context.Context, task.Plan, TaskControl) error
}
