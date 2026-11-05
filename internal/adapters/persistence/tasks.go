package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
)

// SavePlan stores only backend-authored plans in the current workspace.
func (s *Store) SavePlan(ctx context.Context, plan task.Plan) error {
	if plan.WorkspaceID != s.info.WorkspaceID || plan.ID == "" || plan.Fingerprint == "" {
		return problem.New("PLAN_INVALID", "")
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	var revision any
	if plan.ProfileRef != nil {
		revision = plan.ProfileRef.ExpectedRevision
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO plans(plan_id,workspace_id,project_revision,fingerprint,payload,state) VALUES(?,?,?,?,?,'ready')", plan.ID, plan.WorkspaceID, revision, plan.Fingerprint, data)
	return err
}

// LoadPlan refuses invalidated plans after workspace relocation.
func (s *Store) LoadPlan(ctx context.Context, id task.PlanID) (task.Plan, error) {
	var plan task.Plan
	var data []byte
	var state string
	err := s.db.QueryRowContext(ctx, "SELECT payload,state FROM plans WHERE plan_id=? AND workspace_id=?", id, s.info.WorkspaceID).Scan(&data, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return plan, problem.New("PLAN_MISSING", "")
	}
	if err != nil {
		return plan, err
	}
	if state != "ready" {
		return plan, problem.New("PLAN_STALE", "")
	}
	if json.Unmarshal(data, &plan) != nil || plan.ID != id {
		return plan, problem.New("PLAN_INVALID", "")
	}
	return plan, nil
}

// FindTaskRequest supports durable idempotence before revalidating changed inputs.
func (s *Store) FindTaskRequest(ctx context.Context, requestID string) (task.Record, error) {
	return scanTask(s.db.QueryRowContext(ctx, "SELECT payload FROM tasks WHERE request_id=?", requestID))
}

// CreateTask atomically persists the initial state and first event before execution.
func (s *Store) CreateTask(ctx context.Context, record task.Record) (task.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.Record{}, err
	}
	defer tx.Rollback()
	existing, err := scanTask(tx.QueryRowContext(ctx, "SELECT payload FROM tasks WHERE request_id=?", record.RequestID))
	if err == nil {
		if existing.RequestHash != record.RequestHash {
			return task.Record{}, problem.New("REQUEST_CONFLICT", "")
		}
		return existing, nil
	}
	if err.Error() != "TASK_MISSING" {
		return task.Record{}, err
	}
	record.View.State = task.Queued
	record.View.LastEventSeq = "1"
	record.View.Cancelable = true
	data, err := json.Marshal(record)
	if err != nil {
		return task.Record{}, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO tasks(task_id,request_id,plan_id,kind,state,last_event_seq,request_hash,payload,created_at) VALUES(?,?,?,?,?,?,?,?,?)", record.View.ID, record.RequestID, record.PlanID, record.View.Kind, record.View.State, record.View.LastEventSeq, record.RequestHash, data, record.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return task.Record{}, err
	}
	if err := insertTaskEvent(ctx, tx, record, task.EventData{Code: "TASK_QUEUED"}); err != nil {
		return task.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return task.Record{}, err
	}
	return record, nil
}

// Task loads a fresh durable record; no process state is inferred from a task row.
func (s *Store) Task(ctx context.Context, id task.ID) (task.Record, error) {
	return scanTask(s.db.QueryRowContext(ctx, "SELECT payload FROM tasks WHERE task_id=?", id))
}

type rowScanner interface{ Scan(...any) error }

func scanTask(row rowScanner) (task.Record, error) {
	var record task.Record
	var data []byte
	err := row.Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return record, problem.New("TASK_MISSING", "")
	}
	if err != nil {
		return record, err
	}
	if json.Unmarshal(data, &record) != nil || record.View.ID == "" {
		return record, problem.New("TASK_RECORD_INVALID", "")
	}
	return record, nil
}

// QueuedTasks returns candidates for the single workspace mutation worker.
func (s *Store) QueuedTasks(ctx context.Context) ([]task.Record, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT payload FROM tasks WHERE state='queued' ORDER BY created_at,task_id LIMIT 1000")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []task.Record{}
	for rows.Next() {
		record, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

// TransitionTask compares the event cursor and validates the state edge transactionally.
func (s *Store) TransitionTask(ctx context.Context, id task.ID, expected string, to task.State, failure *task.AppError, event task.EventData) (task.Record, error) {
	return s.mutateTask(ctx, id, func(record *task.Record) error {
		if record.View.LastEventSeq != expected {
			return problem.New("TASK_CONFLICT", "")
		}
		if !task.CanTransition(record.View.State, to) {
			return problem.New("TASK_STATE_INVALID", "")
		}
		record.View.State = to
		record.View.Error = failure
		record.View.Cancelable = !task.Terminal(to)
		return nil
	}, event)
}

// AppendTaskEvent persists controlled progress and advances the same optimistic cursor.
func (s *Store) AppendTaskEvent(ctx context.Context, id task.ID, event task.EventData) (task.Record, error) {
	return s.mutateTask(ctx, id, func(record *task.Record) error {
		if task.Terminal(record.View.State) {
			return problem.New("TASK_STATE_INVALID", "")
		}
		return nil
	}, event)
}

func (s *Store) mutateTask(ctx context.Context, id task.ID, mutate func(*task.Record) error, event task.EventData) (task.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.Record{}, err
	}
	defer tx.Rollback()
	record, err := scanTask(tx.QueryRowContext(ctx, "SELECT payload FROM tasks WHERE task_id=?", id))
	if err != nil {
		return record, err
	}
	if err := mutate(&record); err != nil {
		return record, err
	}
	current, err := strconv.ParseInt(record.View.LastEventSeq, 10, 64)
	if err != nil || current < 1 || current == math.MaxInt64 {
		return record, problem.New("TASK_EVENT_LIMIT", "")
	}
	record.View.LastEventSeq = strconv.FormatInt(current+1, 10)
	data, err := json.Marshal(record)
	if err != nil {
		return record, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE tasks SET state=?,last_event_seq=?,payload=? WHERE task_id=?", record.View.State, record.View.LastEventSeq, data, id); err != nil {
		return record, err
	}
	if err := insertTaskEvent(ctx, tx, record, event); err != nil {
		return record, err
	}
	if task.Terminal(record.View.State) {
		if _, err := tx.ExecContext(ctx, "DELETE FROM task_claims WHERE task_id=?", id); err != nil {
			return record, err
		}
	}
	if err := tx.Commit(); err != nil {
		return record, err
	}
	return record, nil
}

func insertTaskEvent(ctx context.Context, tx *sql.Tx, record task.Record, data task.EventData) error {
	if len(data.Code) > 128 || len(data.StepID) > 128 || len(data.Message) > 2048 {
		return problem.New("TASK_EVENT_INVALID", "")
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO task_events(task_id,seq,kind,occurred_at,payload) VALUES(?,?,?,?,?)", record.View.ID, record.View.LastEventSeq, string(record.View.State), time.Now().UTC().Format(time.RFC3339Nano), payload)
	return err
}

// TaskEvents returns at most 2000 events after a validated decimal cursor.
func (s *Store) TaskEvents(ctx context.Context, id task.ID, after string) ([]task.Event, error) {
	cursor := int64(0)
	if after != "" {
		var err error
		cursor, err = strconv.ParseInt(after, 10, 64)
		if err != nil || cursor < 0 || strconv.FormatInt(cursor, 10) != after {
			return nil, problem.New("TASK_CURSOR_INVALID", "")
		}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT seq,kind,occurred_at,payload FROM task_events WHERE task_id=? AND seq>? ORDER BY seq LIMIT 2000", id, cursor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []task.Event{}
	for rows.Next() {
		event := task.Event{TaskID: id}
		var seq int64
		var stamp string
		var data []byte
		if err := rows.Scan(&seq, &event.Kind, &stamp, &data); err != nil {
			return nil, err
		}
		event.Seq = strconv.FormatInt(seq, 10)
		var err error
		event.OccurredAt, err = time.Parse(time.RFC3339Nano, stamp)
		if err != nil || json.Unmarshal(data, &event.Payload) != nil {
			return nil, problem.New("TASK_EVENT_INVALID", "")
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

// RecoverTasks interrupts uncertain operations without replaying any effects.
func (s *Store) RecoverTasks(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT task_id FROM tasks WHERE state IN ('queued','running','waiting-user','cancel-requested')")
	if err != nil {
		return 0, err
	}
	ids := []task.ID{}
	for rows.Next() {
		var id task.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		record, err := s.Task(ctx, id)
		if err != nil {
			return 0, err
		}
		if _, err := s.TransitionTask(ctx, id, record.View.LastEventSeq, task.Interrupted, &task.AppError{Code: "TASK_INTERRUPTED", Message: "Check actual resources before submitting a new operation.", Retryable: true}, task.EventData{Code: "TASK_INTERRUPTED"}); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// ReserveTaskResources preserves claims while a task waits for the user.
func (s *Store) ReserveTaskResources(ctx context.Context, id task.ID, resources []string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	for _, resource := range resources {
		var owner string
		err := tx.QueryRowContext(ctx, "SELECT task_id FROM task_claims WHERE resource_id=?", resource).Scan(&owner)
		if err == nil && owner != string(id) {
			return false, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO task_claims(resource_id,task_id) VALUES(?,?) ON CONFLICT(resource_id) DO NOTHING", resource, id); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ReleaseTaskResources removes only this task's reservations.
func (s *Store) ReleaseTaskResources(ctx context.Context, id task.ID) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM task_claims WHERE task_id=?", id)
	return err
}

// SetTaskResumeAction records the user's action without inventing another task ID.
func (s *Store) SetTaskResumeAction(ctx context.Context, id task.ID, action string) error {
	_, err := s.mutateTask(ctx, id, func(record *task.Record) error {
		if record.View.State != task.WaitingUser {
			return problem.New("TASK_STATE_INVALID", "")
		}
		record.ResumeAction = action
		return nil
	}, task.EventData{Code: "TASK_RESOLVE_REQUESTED"})
	return err
}
