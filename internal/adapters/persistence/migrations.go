package persistence

import (
	"context"
	"database/sql"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
)

const currentDBVersion = 3

const activeStateQuery = `
SELECT EXISTS(SELECT 1 FROM runs WHERE state NOT IN ('stopped','failed-start','detached'))
OR EXISTS(SELECT 1 FROM tasks WHERE state IN ('queued','running','waiting-user','cancel-requested'))
OR EXISTS(
    SELECT 1 FROM resources
    WHERE control_grant<>''
    AND CASE WHEN json_valid(locator_json)
        THEN json_extract(locator_json,'$.active')=1
            OR coalesce(json_extract(locator_json,'$.pendingStart'),'')<>''
        ELSE 1 END
)`

// schemaCurrent stores only manager state. Business databases are never opened here.
const schemaCurrent = `
CREATE TABLE workspace_meta(workspace_id TEXT PRIMARY KEY, db_schema_version INTEGER NOT NULL, last_bound_root TEXT NOT NULL);
CREATE TABLE projects(project_id TEXT PRIMARY KEY, name TEXT NOT NULL, current_revision INTEGER NOT NULL);
CREATE TABLE project_revisions(project_id TEXT NOT NULL, revision INTEGER NOT NULL, document_json BLOB NOT NULL, content_hash TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(project_id,revision));
CREATE TABLE bindings(workspace_id TEXT NOT NULL, project_id TEXT NOT NULL, binding_key TEXT NOT NULL, kind TEXT NOT NULL, local_locator TEXT NOT NULL, secret_ref TEXT NOT NULL, PRIMARY KEY(workspace_id,project_id,binding_key));
CREATE TABLE revision_journal(project_id TEXT NOT NULL, revision INTEGER NOT NULL, relative_path TEXT NOT NULL, owner_token TEXT NOT NULL, PRIMARY KEY(project_id,revision));
CREATE TABLE revision_files(project_id TEXT NOT NULL, revision INTEGER NOT NULL, relative_path TEXT NOT NULL, sha256 TEXT NOT NULL, size INTEGER NOT NULL, PRIMARY KEY(project_id,revision,relative_path));
CREATE TABLE resources(resource_id TEXT PRIMARY KEY, locator_json TEXT NOT NULL, control_grant TEXT NOT NULL, fingerprint TEXT NOT NULL);
CREATE TABLE tasks(task_id TEXT PRIMARY KEY, request_id TEXT NOT NULL UNIQUE, plan_id TEXT NOT NULL, kind TEXT NOT NULL, state TEXT NOT NULL, last_event_seq TEXT NOT NULL,request_hash TEXT NOT NULL,payload BLOB NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE task_events(task_id TEXT NOT NULL,seq INTEGER NOT NULL,kind TEXT NOT NULL,occurred_at TEXT NOT NULL,payload BLOB NOT NULL,PRIMARY KEY(task_id,seq));
CREATE TABLE task_claims(resource_id TEXT PRIMARY KEY,task_id TEXT NOT NULL);
CREATE TABLE plans(plan_id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, project_revision INTEGER, fingerprint TEXT NOT NULL, payload BLOB NOT NULL, state TEXT NOT NULL);
CREATE TABLE runs(run_id TEXT PRIMARY KEY, project_id TEXT NOT NULL, profile_id TEXT NOT NULL, revision INTEGER NOT NULL, immutable_plan BLOB NOT NULL, identity_json BLOB NOT NULL, state TEXT NOT NULL);
CREATE TABLE import_journal(preview_id TEXT PRIMARY KEY, staging TEXT NOT NULL, target_json BLOB NOT NULL, phase TEXT NOT NULL);
`

// upgradeDatabase never invents ownership for legacy pending recovery entries.
func upgradeDatabase(ctx context.Context, db *sql.DB, version int) error {
	if version == currentDBVersion {
		return nil
	}
	if version != 1 && version != 2 {
		return problem.New("WORKSPACE_VERSION_UNSUPPORTED", "")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	var active bool
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM revision_journal").Scan(&count); err != nil {
		return err
	}
	if version == 1 && count != 0 {
		return problem.New("WORKSPACE_RECOVERY_REQUIRED", "")
	}
	if err := tx.QueryRowContext(ctx, activeStateQuery).Scan(&active); err != nil {
		return err
	}
	if active {
		return problem.New("WORKSPACE_BUSY", "")
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info(revision_journal)")
	if err != nil {
		return err
	}
	hasOwner := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "owner_token" {
			hasOwner = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !hasOwner {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE revision_journal ADD COLUMN owner_token TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	rows, err = tx.QueryContext(ctx, "PRAGMA table_info(tasks)")
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, column := range []struct{ name, definition string }{{"request_hash", "TEXT NOT NULL DEFAULT ''"}, {"payload", "BLOB NOT NULL DEFAULT '{}'"}, {"created_at", "TEXT NOT NULL DEFAULT ''"}} {
		if !columns[column.name] {
			if _, err := tx.ExecContext(ctx, "ALTER TABLE tasks ADD COLUMN "+column.name+" "+column.definition); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS task_events(task_id TEXT NOT NULL,seq INTEGER NOT NULL,kind TEXT NOT NULL,occurred_at TEXT NOT NULL,payload BLOB NOT NULL,PRIMARY KEY(task_id,seq));CREATE TABLE IF NOT EXISTS task_claims(resource_id TEXT PRIMARY KEY,task_id TEXT NOT NULL);`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE workspace_meta SET db_schema_version=?", currentDBVersion); err != nil {
		return err
	}
	return tx.Commit()
}
