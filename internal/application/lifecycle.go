package application

import (
	"context"
	"sort"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// LifecycleService owns explicit Quit. Hiding a desktop window never calls it.
// The coordinator closes task admission before cancelling queued/preparation work.
type LifecycleService struct {
	workspaces *WorkspaceService
	tasks      *TaskService
	stop       *StopExecutor
}

// NewLifecycleService must be wired before the desktop can submit tasks.
func NewLifecycleService(ws *WorkspaceService, tasks *TaskService, stop *StopExecutor) *LifecycleService {
	service := &LifecycleService{workspaces: ws, tasks: tasks, stop: stop}
	tasks.executors[task.Exit] = service
	return service
}

// RequestExit is idempotent until abort-exit completes. It creates a backend-only
// plan; clients cannot forge exit tasks via Submit. The worker remains serial, so
// an active prepare reaches its safe cancellation point before any stop begins.
func (s *LifecycleService) RequestExit(ctx context.Context) (TaskView, error) {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo := s.workspaces.current
	if repo == nil {
		return TaskView{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	store, err := taskStorage(repo)
	if err != nil {
		return TaskView{}, err
	}
	info := repo.Info()
	if info.State != "ready" {
		return TaskView{}, problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	if err = s.tasks.ensureRecovered(ctx, store, info.WorkspaceID+"|"+info.Root); err != nil {
		return TaskView{}, err
	}
	s.tasks.admissionMu.Lock()
	defer s.tasks.admissionMu.Unlock()
	if s.tasks.exitID != "" {
		record, err := store.Task(ctx, s.tasks.exitID)
		return record.View, err
	}
	id, err := identity.NewUUID()
	if err != nil {
		return TaskView{}, err
	}
	plan := task.Plan{
		ID:          task.PlanID(id),
		Kind:        task.Exit,
		WorkspaceID: info.WorkspaceID,
		Fingerprint: "exit:" + info.WorkspaceID,
		CreatedAt:   time.Now().UTC(),
	}
	claims := map[string]bool{}
	if runs, ok := repo.(ports.RunRepository); ok {
		active, err := runs.ActiveRuns(ctx)
		if err != nil {
			return TaskView{}, err
		}
		for _, record := range active {
			for _, unit := range record.Snapshot.Units {
				claims[unit.ResourceID] = true
			}
		}
	}
	if resources, ok := repo.(ports.ResourceRepository); ok {
		records, err := resources.ListResources(ctx)
		if err != nil {
			return TaskView{}, err
		}
		for _, record := range records {
			if record.Control == "managed" {
				claims[record.ResourceID] = true
			}
		}
	}
	pending, err := store.PendingTasks(ctx)
	if err != nil {
		return TaskView{}, err
	}
	for _, record := range pending {
		previous, err := store.LoadPlan(ctx, record.PlanID)
		if err != nil {
			return TaskView{}, err
		}
		for _, resourceID := range previous.ResourceIDs {
			claims[resourceID] = true
		}
	}
	for resourceID := range claims {
		if resourceID != "" {
			plan.ResourceIDs = append(plan.ResourceIDs, resourceID)
		}
	}
	sort.Strings(plan.ResourceIDs)
	if err = store.SavePlan(ctx, plan); err != nil {
		return TaskView{}, err
	}
	if err = quiesceForExit(ctx, store); err != nil {
		return TaskView{}, err
	}
	record, err := store.CreateTask(
		ctx,
		task.Record{
			View:        task.View{ID: task.ID(id), Kind: task.Exit},
			PlanID:      plan.ID,
			RequestID:   "exit:" + id,
			RequestHash: plan.Fingerprint,
			CreatedAt:   time.Now().UTC(),
		},
	)
	if err != nil {
		return TaskView{}, err
	}
	s.tasks.exitID = record.View.ID
	s.tasks.notify()
	return record.View, nil
}

func quiesceForExit(ctx context.Context, store ports.TaskRepository) error {
	pending, err := store.PendingTasks(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range pending {
		for {
			record, err := store.Task(ctx, candidate.View.ID)
			if err != nil {
				return err
			}
			if task.Terminal(record.View.State) || record.View.State == task.CancelRequested {
				break
			}
			to := task.CancelRequested
			if record.View.State == task.Queued || record.View.State == task.WaitingUser {
				to = task.Cancelled
			}
			_, err = store.TransitionTask(
				ctx,
				record.View.ID,
				record.View.LastEventSeq,
				to,
				nil,
				task.EventData{Code: "EXIT_CANCEL_REQUESTED"},
			)
			if err == nil {
				break
			}
			if err.Error() != "TASK_CONFLICT" {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Execute stops projects before unused shared services. Failed normal stops yield
// the execution slot but preserve admission closure and every unresolved run.
func (s *LifecycleService) Execute(ctx context.Context, plan task.Plan, control ports.TaskControl) error {
	store, err := s.stop.repository()
	if err != nil {
		return err
	}
	active, err := store.ActiveRuns(ctx)
	if err != nil {
		return err
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Snapshot.CreatedAt.After(active[j].Snapshot.CreatedAt) })
	for i := range active {
		if err = s.stop.stopRun(ctx, store, &active[i], false, false, control); err != nil {
			return err
		}
	}
	s.workspaces.mu.RLock()
	resources, ok := s.workspaces.current.(ports.ResourceRepository)
	s.workspaces.mu.RUnlock()
	if ok {
		records, err := resources.ListResources(ctx)
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.Control != "managed" || (!record.Active && record.PendingStart == "") {
				continue
			}
			if s.stop.resources == nil {
				return &task.WaitingError{
					Code:    "RESOURCE_ADAPTER_UNAVAILABLE",
					Message: "Inspect unresolved resources before quitting.",
				}
			}
			result, err := s.stop.resources.StopUnused(ctx, record.ResourceID, true)
			if err != nil {
				return &task.WaitingError{
					Code:    safeTaskError(err).Code,
					Message: "A managed resource could not stop normally.",
				}
			}
			if !result.Stopped {
				return &task.WaitingError{
					Code:    "RESOURCE_STOP_INCOMPLETE",
					Message: "A managed resource remains in use or has an uncertain operation.",
				}
			}
		}
	}
	return control.Progress(ctx, task.EventData{Code: "MANAGER_ALLOW_QUIT"})
}

// CanQuit is the bootstrap's final gate. An event alone does not authorize closing
// the core: the same exit task must also have committed its successful state.
func (s *LifecycleService) CanQuit(ctx context.Context) (bool, error) {
	s.tasks.admissionMu.Lock()
	id := s.tasks.exitID
	s.tasks.admissionMu.Unlock()
	if id == "" {
		return false, nil
	}
	view, err := s.tasks.Get(ctx, id)
	return err == nil && view.State == task.Succeeded, err
}
