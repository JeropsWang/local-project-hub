package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

// CreateRun publishes immutable launch intent before acquiring any external unit.
// One active run per project prevents overlapping profiles from sharing process
// resources accidentally. Different projects still use explicit resource users.
func (s *Store) CreateRun(ctx context.Context, snapshot run.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.State != "ready" {
		return problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	if snapshot.ID == "" || snapshot.ProfileRef.ProjectID == "" || snapshot.ProfileRef.ExpectedRevision < 1 {
		return problem.New("RUN_INVALID", "")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM runs WHERE run_id=? OR (project_id=? AND state NOT IN ('stopped','failed-start','detached')))", snapshot.ID, snapshot.ProfileRef.ProjectID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return problem.New("RUN_ALREADY_ACTIVE", "")
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	progress, _ := json.Marshal(run.Record{Version: 1, State: "starting", Acquisitions: []run.Acquisition{}})
	_, err = tx.ExecContext(
		ctx,
		"INSERT INTO runs(run_id,project_id,profile_id,revision,immutable_plan,identity_json,state) VALUES(?,?,?,?,?,?,'starting')",
		snapshot.ID,
		snapshot.ProfileRef.ProjectID,
		snapshot.ProfileRef.ProfileID,
		snapshot.ProfileRef.ExpectedRevision,
		data,
		progress,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func scanRun(row rowScanner) (run.Record, error) {
	var record run.Record
	var immutable, progress []byte
	var state string
	if err := row.Scan(&immutable, &progress, &state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return record, problem.New("RUN_MISSING", "")
		}
		return record, err
	}
	if json.Unmarshal(progress, &record) != nil || json.Unmarshal(immutable, &record.Snapshot) != nil ||
		record.Version < 1 ||
		record.State != state {
		return run.Record{}, problem.New("RUN_INVALID", "")
	}
	return record, nil
}

// Run always reads the original launch snapshot, including its stop scripts.
func (s *Store) Run(ctx context.Context, id string) (run.Record, error) {
	return scanRun(s.db.QueryRowContext(ctx, "SELECT immutable_plan,identity_json,state FROM runs WHERE run_id=?", id))
}

// ActiveRuns includes uncertain starts/stops, newest first for reverse shutdown.
func (s *Store) ActiveRuns(ctx context.Context) ([]run.Record, error) {
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT immutable_plan,identity_json,state FROM runs WHERE state NOT IN ('stopped','failed-start','detached')",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []run.Record{}
	for rows.Next() {
		record, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

// interruptRuns records loss of live supervision after opening an existing local
// workspace. Stored PIDs do not reconstruct kernel handles or replay side effects.
func (s *Store) interruptRuns(ctx context.Context) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE runs SET state='interrupted',identity_json=CASE WHEN json_valid(identity_json) THEN json_set(identity_json,'$.state','interrupted','$.version',coalesce(json_extract(identity_json,'$.version'),0)+1) ELSE identity_json END WHERE state NOT IN ('stopped','failed-start','detached','interrupted')`,
	)
	return err
}

// recordedRunSummary returns saved observation; an unverified host cannot display
// imported origin rows as its own running applications.
func (s *Store) recordedRunSummary(ctx context.Context, projectID string) (string, error) {
	if !s.ownerVerified.Load() {
		return "unverified", nil
	}
	var state string
	err := s.db.QueryRowContext(ctx, "SELECT state FROM runs WHERE project_id=? AND state NOT IN ('stopped','failed-start','detached') LIMIT 1", projectID).
		Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "stopped", nil
	}
	return state, err
}

// UpdateRun changes only the journal; it cannot replace the immutable snapshot.
// The compare-and-swap prevents delayed observers from overwriting newer evidence.
func (s *Store) UpdateRun(ctx context.Context, record run.Record, expected int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.State != "ready" {
		return problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	switch record.State {
	case "starting", "running", "stopping", "stop-failed", "interrupted":
	case "stopped", "failed-start":
		for _, acquisition := range record.Acquisitions {
			if acquisition.Phase != "released" {
				return problem.New("RUN_STILL_ACQUIRED", acquisition.UnitID)
			}
		}
	default:
		return problem.New("RUN_STATE_INVALID", "")
	}
	current, err := s.Run(ctx, record.Snapshot.ID)
	if err != nil {
		return err
	}
	if current.Version != expected {
		return problem.New("RUN_CONFLICT", "")
	}
	if current.State == "stopped" || current.State == "failed-start" || current.State == "detached" {
		return problem.New("RUN_TERMINAL", "")
	}
	record.Version = expected + 1
	record.Snapshot = run.Snapshot{}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(
		ctx,
		"UPDATE runs SET identity_json=?,state=? WHERE run_id=?",
		data,
		record.State,
		current.Snapshot.ID,
	)
	return err
}
