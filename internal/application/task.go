package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// TaskService runs a single mutation worker; waiting tasks return its execution slot.
type TaskService struct {
	workspaces  *WorkspaceService
	plans       *PlanService
	executors   map[task.Kind]ports.TaskExecutor
	wake        chan struct{}
	stop        chan struct{}
	done        chan struct{}
	mu          sync.Mutex
	recovered   map[string]bool
	closed      bool
	admissionMu sync.Mutex
	exitID      task.ID
}

// NewTaskService creates one coordinator for the application. Construct it only once.
func NewTaskService(ws *WorkspaceService, plans *PlanService, executors map[task.Kind]ports.TaskExecutor) *TaskService {
	s := &TaskService{
		workspaces: ws,
		plans:      plans,
		executors:  executors,
		wake:       make(chan struct{}, 1),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		recovered:  map[string]bool{},
	}
	go s.worker()
	return s
}

func (s *TaskService) ensureRecovered(ctx context.Context, store ports.TaskRepository, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return problem.New("TASK_SERVICE_CLOSED", "")
	}
	if s.recovered[key] {
		return nil
	}
	if _, err := store.RecoverTasks(ctx); err != nil {
		return err
	}
	s.recovered[key] = true
	return nil
}

func (s *TaskService) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Submit resolves request idempotence before stale-plan validation.
func (s *TaskService) Submit(ctx context.Context, req SubmitPlanRequest) (TaskView, error) {
	if req.RequestID == "" || len(req.RequestID) > 128 {
		return TaskView{}, problem.New("REQUEST_ID_INVALID", "")
	}
	s.admissionMu.Lock()
	exiting := s.exitID != ""
	s.admissionMu.Unlock()
	if exiting {
		return TaskView{}, problem.New("EXIT_IN_PROGRESS", "")
	}
	s.workspaces.mu.RLock()
	repo := s.workspaces.current
	if repo == nil {
		s.workspaces.mu.RUnlock()
		return TaskView{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	store, err := taskStorage(repo)
	if err != nil {
		s.workspaces.mu.RUnlock()
		return TaskView{}, err
	}
	info := repo.Info()
	if err := s.ensureRecovered(ctx, store, info.WorkspaceID+"|"+info.Root); err != nil {
		s.workspaces.mu.RUnlock()
		return TaskView{}, err
	}
	existing, err := store.FindTaskRequest(ctx, req.RequestID)
	if err == nil {
		s.workspaces.mu.RUnlock()
		if existing.RequestHash != requestHash(req) {
			return TaskView{}, problem.New("REQUEST_CONFLICT", "")
		}
		return existing.View, nil
	}
	if err.Error() != "TASK_MISSING" {
		s.workspaces.mu.RUnlock()
		return TaskView{}, err
	}
	plan, err := store.LoadPlan(ctx, req.PlanID)
	s.workspaces.mu.RUnlock()
	if err != nil {
		return TaskView{}, err
	}
	if plan.Fingerprint != req.Fingerprint {
		return TaskView{}, problem.New("PLAN_STALE", "")
	}
	if plan.Kind == task.Exit {
		return TaskView{}, problem.New("EXIT_COORDINATOR_REQUIRED", "")
	}
	if err := s.plans.validate(ctx, plan); err != nil {
		return TaskView{}, err
	}
	id, err := identity.NewUUID()
	if err != nil {
		return TaskView{}, err
	}
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	if s.exitID != "" {
		return TaskView{}, problem.New("EXIT_IN_PROGRESS", "")
	}
	if s.workspaces.current != repo || repo.Info().State != "ready" {
		return TaskView{}, problem.New("PLAN_STALE", "")
	}
	record := task.Record{
		View:        task.View{ID: task.ID(id), Kind: plan.Kind},
		PlanID:      plan.ID,
		RequestID:   req.RequestID,
		RequestHash: requestHash(req),
		CreatedAt:   time.Now().UTC(),
	}
	created, err := store.CreateTask(ctx, record)
	if err != nil {
		return TaskView{}, err
	}
	s.notify()
	return created.View, nil
}

// Get returns durable state and performs one-time recovery for a newly opened workspace.
func (s *TaskService) Get(ctx context.Context, id TaskID) (TaskView, error) {
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
	if err := s.ensureRecovered(ctx, store, info.WorkspaceID+"|"+info.Root); err != nil {
		return TaskView{}, err
	}
	record, err := store.Task(ctx, id)
	return record.View, err
}

// Events recovers progress from a decimal cursor without trusting frontend event state.
func (s *TaskService) Events(ctx context.Context, id TaskID, after EventCursor) ([]Event, error) {
	if _, err := parseCursor(string(after)); err != nil {
		return nil, err
	}
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	if s.workspaces.current == nil {
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	store, err := taskStorage(s.workspaces.current)
	if err != nil {
		return nil, err
	}
	return store.TaskEvents(ctx, id, string(after))
}

// Cancel records intent. Running work stops only when its executor reaches a safe point.
func (s *TaskService) Cancel(ctx context.Context, id TaskID) (TaskView, error) {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	if s.workspaces.current == nil {
		return TaskView{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	store, err := taskStorage(s.workspaces.current)
	if err != nil {
		return TaskView{}, err
	}
	for range 5 {
		record, err := store.Task(ctx, id)
		if err != nil {
			return TaskView{}, err
		}
		if task.Terminal(record.View.State) || record.View.State == task.CancelRequested {
			return record.View, nil
		}
		if record.View.Kind == task.Exit {
			return TaskView{}, problem.New("EXIT_RESOLUTION_REQUIRED", "")
		}
		to := task.CancelRequested
		if record.View.State == task.Queued || record.View.State == task.WaitingUser {
			if record.View.Kind == task.Exit && record.View.State == task.WaitingUser {
				return TaskView{}, problem.New("EXIT_RESOLUTION_REQUIRED", "")
			}
			to = task.Cancelled
		}
		updated, err := store.TransitionTask(
			ctx,
			id,
			record.View.LastEventSeq,
			to,
			nil,
			task.EventData{Code: "TASK_CANCEL_REQUESTED"},
		)
		if err == nil {
			s.notify()
			return updated.View, nil
		}
		if err.Error() != "TASK_CONFLICT" {
			return TaskView{}, err
		}
	}
	return TaskView{}, problem.New("TASK_CONFLICT", "")
}

// Resolve requeues the same waiting task. Lifecycle coordinates exit admission separately.
func (s *TaskService) Resolve(ctx context.Context, req ResolveTaskRequest) (TaskView, error) {
	if req.Action != "retry" && req.Action != "continue-wait" && req.Action != "abort-exit" {
		return TaskView{}, problem.New("TASK_ACTION_INVALID", "")
	}
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	if s.workspaces.current == nil {
		return TaskView{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	store, err := taskStorage(s.workspaces.current)
	if err != nil {
		return TaskView{}, err
	}
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	if s.exitID != "" && req.TaskID != s.exitID {
		return TaskView{}, problem.New("EXIT_IN_PROGRESS", "")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := store.Task(ctx, req.TaskID)
	if err != nil {
		return TaskView{}, err
	}
	if record.View.State != task.WaitingUser || record.View.LastEventSeq != req.ExpectedEventSeq {
		return TaskView{}, problem.New("TASK_CONFLICT", "")
	}
	if req.Action == "abort-exit" && record.View.Kind != task.Exit {
		return TaskView{}, problem.New("TASK_ACTION_INVALID", "")
	}
	record, err = store.ResolveTask(ctx, req.TaskID, req.ExpectedEventSeq, req.Action)
	if err == nil {
		if req.Action == "abort-exit" {
			s.exitID = ""
		}
		s.notify()
	}
	return record.View, err
}

func (s *TaskService) worker() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		case <-s.wake:
			s.drain()
		}
	}
}

func (s *TaskService) drain() {
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		s.workspaces.mu.RLock()
		repo := s.workspaces.current
		if repo == nil {
			s.workspaces.mu.RUnlock()
			return
		}
		store, err := taskStorage(repo)
		if err != nil {
			s.workspaces.mu.RUnlock()
			return
		}
		candidates, err := store.QueuedTasks(context.Background())
		if err != nil {
			s.workspaces.mu.RUnlock()
			return
		}
		var selected *task.Record
		var selectedPlan task.Plan
		s.admissionMu.Lock()
		for _, candidate := range candidates {
			if s.exitID != "" && candidate.View.ID != s.exitID {
				continue
			}
			plan, err := store.LoadPlan(context.Background(), candidate.PlanID)
			if err != nil {
				s.finish(store, candidate.View.ID, task.Failed, safeTaskError(err))
				continue
			}
			acquired, err := store.ReserveTaskResources(context.Background(), candidate.View.ID, plan.ResourceIDs)
			if err != nil || !acquired {
				continue
			}
			updated, err := store.TransitionTask(
				context.Background(),
				candidate.View.ID,
				candidate.View.LastEventSeq,
				task.Running,
				nil,
				task.EventData{Code: "TASK_RUNNING"},
			)
			if err != nil {
				_ = store.ReleaseTaskResources(context.Background(), candidate.View.ID)
				continue
			}
			selected = &updated
			selectedPlan = plan
			break
		}
		s.admissionMu.Unlock()
		s.workspaces.mu.RUnlock()
		if selected == nil {
			return
		}
		s.execute(store, *selected, selectedPlan)
	}
}

func (s *TaskService) execute(store ports.TaskRepository, record task.Record, plan task.Plan) {
	ctx := context.Background()
	if err := s.plans.validate(ctx, plan); err != nil {
		s.finish(store, record.View.ID, task.Failed, safeTaskError(err))
		return
	}
	executor := s.executors[plan.Kind]
	if executor == nil {
		s.finish(
			store,
			record.View.ID,
			task.Failed,
			&task.AppError{
				Code:    "TASK_EXECUTOR_UNAVAILABLE",
				Message: "This operation is not implemented by the current adapter.",
			},
		)
		return
	}
	control := &taskControl{store: store, id: record.View.ID, resume: record.ResumeAction}
	if err := control.Checkpoint(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			s.finish(store, record.View.ID, task.Cancelled, nil)
		} else {
			s.finish(store, record.View.ID, task.Failed, safeTaskError(err))
		}
		return
	}
	err := invokeExecutor(ctx, executor, plan, control)
	if err == nil {
		s.finish(store, record.View.ID, task.Succeeded, nil)
		return
	}
	if errors.Is(err, context.Canceled) {
		s.finish(store, record.View.ID, task.Cancelled, nil)
		return
	}
	var waiting *task.WaitingError
	if errors.As(err, &waiting) {
		s.admissionMu.Lock()
		defer s.admissionMu.Unlock()
		// A normal stop may yield after Quit requested cancellation at a safe
		// point. Its run journal still owns the unfinished business operation;
		// release the old task's claims so the sole exit task can continue it.
		// Native stop reuses the frozen operation ID and never resends a save.
		current, readErr := store.Task(ctx, record.View.ID)
		if s.exitID != "" && plan.Kind != task.Exit && readErr == nil && current.View.State == task.CancelRequested {
			s.finish(store, record.View.ID, task.Cancelled, nil)
			return
		}
		s.finish(
			store,
			record.View.ID,
			task.WaitingUser,
			&task.AppError{Code: waiting.Code, Message: waiting.Message, Retryable: true},
		)
		return
	}
	s.finish(store, record.View.ID, task.Failed, safeTaskError(err))
}

// invokeExecutor keeps an adapter bug from crashing the manager and losing supervision.
// The executor still owns rollback of resources it acquired before returning or panicking.
func invokeExecutor(
	ctx context.Context,
	executor ports.TaskExecutor,
	plan task.Plan,
	control ports.TaskControl,
) (err error) {
	defer func() {
		if recover() != nil {
			err = problem.New("TASK_EXECUTOR_PANICKED", "")
		}
	}()
	return executor.Execute(ctx, plan, control)
}

func (s *TaskService) finish(store ports.TaskRepository, id task.ID, state task.State, failure *task.AppError) {
	for range 5 {
		record, err := store.Task(context.Background(), id)
		if err != nil || task.Terminal(record.View.State) {
			return
		}
		_, err = store.TransitionTask(
			context.Background(),
			id,
			record.View.LastEventSeq,
			state,
			failure,
			task.EventData{Code: "TASK_" + strings.ToUpper(strings.ReplaceAll(string(state), "-", "_"))},
		)
		if err == nil {
			return
		}
		if err.Error() != "TASK_CONFLICT" {
			return
		}
	}
}

type taskControl struct {
	store  ports.TaskRepository
	id     task.ID
	resume string
}

func (c *taskControl) Checkpoint(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	record, err := c.store.Task(ctx, c.id)
	if err != nil {
		return err
	}
	if record.View.State == task.CancelRequested || record.View.State == task.Cancelled {
		return context.Canceled
	}
	return nil
}
func (c *taskControl) Progress(ctx context.Context, event task.EventData) error {
	_, err := c.store.AppendTaskEvent(ctx, c.id, event)
	return err
}
func (c *taskControl) ResumeAction() string { return c.resume }

// Close releases the worker only when no managed activity can be detached.
func (s *TaskService) Close(ctx context.Context) error {
	s.workspaces.mu.RLock()
	if s.workspaces.current != nil {
		active, err := s.workspaces.current.HasActive(ctx)
		if err != nil {
			s.workspaces.mu.RUnlock()
			return err
		}
		if active {
			s.workspaces.mu.RUnlock()
			return problem.New("WORKSPACE_BUSY", "")
		}
	}
	s.workspaces.mu.RUnlock()
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.stop)
	}
	s.mu.Unlock()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
