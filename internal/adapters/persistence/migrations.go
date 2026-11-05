package persistence

// schemaV1 stores only manager state. Business databases are never opened here.
const schemaV1 = `
CREATE TABLE workspace_meta(workspace_id TEXT PRIMARY KEY, db_schema_version INTEGER NOT NULL, last_bound_root TEXT NOT NULL);
CREATE TABLE projects(project_id TEXT PRIMARY KEY, name TEXT NOT NULL, current_revision INTEGER NOT NULL);
CREATE TABLE project_revisions(project_id TEXT NOT NULL, revision INTEGER NOT NULL, document_json BLOB NOT NULL, content_hash TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(project_id,revision));
CREATE TABLE bindings(workspace_id TEXT NOT NULL, project_id TEXT NOT NULL, binding_key TEXT NOT NULL, kind TEXT NOT NULL, local_locator TEXT NOT NULL, secret_ref TEXT NOT NULL, PRIMARY KEY(workspace_id,project_id,binding_key));
CREATE TABLE revision_journal(project_id TEXT NOT NULL, revision INTEGER NOT NULL, relative_path TEXT NOT NULL, PRIMARY KEY(project_id,revision));
CREATE TABLE revision_files(project_id TEXT NOT NULL, revision INTEGER NOT NULL, relative_path TEXT NOT NULL, sha256 TEXT NOT NULL, size INTEGER NOT NULL, PRIMARY KEY(project_id,revision,relative_path));
CREATE TABLE resources(resource_id TEXT PRIMARY KEY, locator_json TEXT NOT NULL, control_grant TEXT NOT NULL, fingerprint TEXT NOT NULL);
CREATE TABLE tasks(task_id TEXT PRIMARY KEY, request_id TEXT NOT NULL UNIQUE, plan_id TEXT NOT NULL, kind TEXT NOT NULL, state TEXT NOT NULL, last_event_seq TEXT NOT NULL);
CREATE TABLE plans(plan_id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, project_revision INTEGER, fingerprint TEXT NOT NULL, payload BLOB NOT NULL, state TEXT NOT NULL);
CREATE TABLE runs(run_id TEXT PRIMARY KEY, project_id TEXT NOT NULL, profile_id TEXT NOT NULL, revision INTEGER NOT NULL, immutable_plan BLOB NOT NULL, identity_json BLOB NOT NULL, state TEXT NOT NULL);
CREATE TABLE import_journal(preview_id TEXT PRIMARY KEY, staging TEXT NOT NULL, target_json BLOB NOT NULL, phase TEXT NOT NULL);
`
