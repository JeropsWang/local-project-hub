package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strconv"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
)

// Detaching a foreign/unverifiable owner never asserts its processes stopped.
// Preserve immutable evidence, interrupt old tasks, revoke claims, then publish
// the new host proof only after the database invalidation transaction commits.
func detachOriginState(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(
		ctx,
		`UPDATE runs SET state='detached',identity_json=CASE WHEN json_valid(identity_json) THEN json_set(identity_json,'$.state','detached','$.version',coalesce(json_extract(identity_json,'$.version'),0)+1) ELSE identity_json END WHERE state NOT IN ('stopped','failed-start','detached')`,
	)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(
		ctx,
		"SELECT payload FROM tasks WHERE state IN ('queued','running','waiting-user','cancel-requested')",
	)
	if err != nil {
		return err
	}
	records := []task.Record{}
	for rows.Next() {
		record, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return err
		}
		records = append(records, record)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, record := range records {
		cursor, err := strconv.ParseInt(record.View.LastEventSeq, 10, 64)
		if err != nil || cursor < 1 || cursor == math.MaxInt64 {
			return problem.New("TASK_EVENT_LIMIT", "")
		}
		record.View.State = task.Interrupted
		record.View.Cancelable = false
		record.View.LastEventSeq = strconv.FormatInt(cursor+1, 10)
		record.View.Error = &task.AppError{
			Code:    "ORIGIN_OWNER_DETACHED",
			Message: "Origin execution evidence is archived; verify and bind this computer separately.",
		}
		data, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE tasks SET state=?,last_event_seq=?,payload=? WHERE task_id=?", record.View.State, record.View.LastEventSeq, data, record.View.ID); err != nil {
			return err
		}
		if err = insertTaskEvent(ctx, tx, record, task.EventData{Code: "ORIGIN_OWNER_DETACHED"}); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM task_claims")
	return err
}
