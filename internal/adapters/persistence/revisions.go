package persistence

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"path"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

// Register writes revision 1; identities are never silently replaced.
func (s *Store) Register(ctx context.Context, doc project.Document, data []byte, bindings []project.Binding, files map[string][]byte) (project.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var exists bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects WHERE project_id=?)", doc.ProjectID).Scan(&exists); err != nil {
		return project.Snapshot{}, err
	}
	if exists {
		return project.Snapshot{}, problem.New("PROJECT_EXISTS", "")
	}
	return s.writeRevision(ctx, doc, 0, data, bindings, files)
}

// Save compares the current revision before publishing new immutable files.
func (s *Store) Save(ctx context.Context, doc project.Document, expected int32, data []byte, bindings []project.Binding, files map[string][]byte) (project.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var revision int32
	if err := s.db.QueryRowContext(ctx, "SELECT current_revision FROM projects WHERE project_id=?", doc.ProjectID).Scan(&revision); err != nil {
		return project.Snapshot{}, problem.New("PROJECT_MISSING", "")
	}
	if expected < 1 || expected != revision {
		return project.Snapshot{}, problem.New("REVISION_CONFLICT", "")
	}
	if revision == math.MaxInt32 {
		return project.Snapshot{}, problem.New("REVISION_LIMIT", "")
	}
	return s.writeRevision(ctx, doc, revision, data, bindings, files)
}

func (s *Store) writeRevision(ctx context.Context, doc project.Document, previous int32, data []byte, bindings []project.Binding, files map[string][]byte) (project.Snapshot, error) {
	revision := previous + 1
	relative := filesystem.RevisionPath(doc.ProjectID, revision)
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return project.Snapshot{}, err
	}
	owner := hex.EncodeToString(nonce[:])
	if _, err := s.db.ExecContext(ctx, "INSERT INTO revision_journal VALUES(?,?,?,?)", doc.ProjectID, revision, relative, owner); err != nil {
		return project.Snapshot{}, err
	}
	files, err := s.resolveFiles(ctx, doc, previous, files)
	var publication filesystem.RevisionPublication
	if err == nil {
		publication, err = s.dir.WriteRevision(doc, revision, owner, files)
	}
	if err != nil {
		// The journal records intent, not ownership of a preexisting directory.
		// Only the publication result proves which staging path this call created.
		if cleanupErr := s.dir.DiscardPublication(publication); cleanupErr != nil {
			return project.Snapshot{}, cleanupErr
		}
		_, _ = s.db.ExecContext(ctx, "DELETE FROM revision_journal WHERE project_id=? AND revision=?", doc.ProjectID, revision)
		return project.Snapshot{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return project.Snapshot{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if previous == 0 {
		_, err = tx.ExecContext(ctx, "INSERT INTO projects VALUES(?,?,?)", doc.ProjectID, doc.Name, revision)
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, "UPDATE projects SET name=?,current_revision=? WHERE project_id=? AND current_revision=?", doc.Name, revision, doc.ProjectID, previous)
		if err == nil {
			n, e := result.RowsAffected()
			err = e
			if n != 1 && err == nil {
				err = problem.New("REVISION_CONFLICT", "")
			}
		}
	}
	if err != nil {
		return project.Snapshot{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO project_revisions VALUES(?,?,?,?,?)", doc.ProjectID, revision, data, hash, now.Format(time.RFC3339Nano)); err != nil {
		return project.Snapshot{}, err
	}
	for name, bytes := range files {
		sum := sha256.Sum256(bytes)
		if _, err := tx.ExecContext(ctx, "INSERT INTO revision_files VALUES(?,?,?,?,?)", doc.ProjectID, revision, name, hex.EncodeToString(sum[:]), len(bytes)); err != nil {
			return project.Snapshot{}, err
		}
	}
	for _, binding := range bindings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO bindings VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_id,project_id,binding_key) DO UPDATE SET kind=excluded.kind,local_locator=excluded.local_locator,secret_ref=excluded.secret_ref`, s.info.WorkspaceID, doc.ProjectID, binding.Key, binding.Kind, binding.LocalLocator, binding.SecretRef); err != nil {
			return project.Snapshot{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM revision_journal WHERE project_id=? AND revision=?", doc.ProjectID, revision); err != nil {
		return project.Snapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return project.Snapshot{}, err
	}
	return project.Snapshot{Document: doc, Revision: revision, ContentHash: hash, CreatedAt: now, CheckSummary: "unverified", RunSummary: "stopped"}, nil
}

// resolveFiles checks old bytes against stored hashes before carrying them forward.
func (s *Store) resolveFiles(ctx context.Context, doc project.Document, previous int32, provided map[string][]byte) (map[string][]byte, error) {
	result := map[string][]byte{}
	for key, value := range provided {
		result[key] = value
	}
	total := 0
	count := 0
	for _, file := range doc.Files {
		if file.Availability != "included" {
			continue
		}
		count++
		data, ok := result[file.BundlePath]
		if !ok && previous > 0 {
			var expected string
			if err := s.db.QueryRowContext(ctx, "SELECT sha256 FROM revision_files WHERE project_id=? AND revision=? AND relative_path=?", doc.ProjectID, previous, file.BundlePath).Scan(&expected); err != nil {
				return nil, problem.New("BUNDLE_FILE_MISSING", file.BundlePath)
			}
			var err error
			data, err = s.dir.Root.ReadFile(path.Join(filesystem.RevisionPath(doc.ProjectID, previous), file.BundlePath))
			if err != nil {
				return nil, problem.New("BUNDLE_FILE_MISSING", file.BundlePath)
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != expected {
				return nil, problem.New("REVISION_FILE_CHANGED", file.BundlePath)
			}
			result[file.BundlePath] = data
			ok = true
		}
		if !ok {
			return nil, problem.New("BUNDLE_FILE_MISSING", file.BundlePath)
		}
		total += len(data)
		if len(data) > 16*1024*1024 || total > 128*1024*1024 || count > 2048 {
			return nil, problem.New("REVISION_FILE_LIMIT", "")
		}
	}
	return result, nil
}

func (s *Store) recoverRevisions(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT project_id,revision,relative_path,owner_token FROM revision_journal")
	if err != nil {
		return err
	}
	type pending struct {
		id       string
		revision int32
		relative string
		owner    string
	}
	entries := []pending{}
	for rows.Next() {
		var entry pending
		if err := rows.Scan(&entry.id, &entry.revision, &entry.relative, &entry.owner); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, entry := range entries {
		if entry.revision < 1 || entry.relative != filesystem.RevisionPath(entry.id, entry.revision) {
			return problem.New("RECOVERY_JOURNAL_INVALID", "")
		}
		var exists bool
		if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM project_revisions WHERE project_id=? AND revision=?)", entry.id, entry.revision).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if err := s.dir.RemoveOwnedRevision(entry.id, entry.revision, entry.owner); err != nil {
				return err
			}
		}
		if _, err := s.db.ExecContext(ctx, "DELETE FROM revision_journal WHERE project_id=? AND revision=?", entry.id, entry.revision); err != nil {
			return err
		}
	}
	return nil
}

// Get verifies configuration bytes before decoding the current immutable revision.
func (s *Store) Get(ctx context.Context, id string) (project.Snapshot, error) {
	var result project.Snapshot
	var data []byte
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT r.document_json,r.revision,r.content_hash,r.created_at FROM project_revisions r JOIN projects p ON p.project_id=r.project_id AND p.current_revision=r.revision WHERE p.project_id=?`, id).Scan(&data, &result.Revision, &result.ContentHash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return result, problem.New("PROJECT_MISSING", "")
	}
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != result.ContentHash {
		return result, problem.New("REVISION_DOCUMENT_CHANGED", "")
	}
	if err := json.Unmarshal(data, &result.Document); err != nil {
		return result, problem.New("REVISION_DOCUMENT_INVALID", "")
	}
	result.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return result, problem.New("REVISION_DOCUMENT_INVALID", "")
	}
	result.CheckSummary = "unverified"
	result.RunSummary = "stopped"
	return result, nil
}

// List returns bounded deterministic results; parameterized filters never become SQL.
func (s *Store) List(ctx context.Context, q project.Query) ([]project.Snapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project_id FROM projects WHERE (?='' OR json_extract((SELECT document_json FROM project_revisions WHERE project_id=projects.project_id AND revision=projects.current_revision),'$.group')=?) AND (?='' OR instr(lower(name),lower(?))>0) ORDER BY name,project_id LIMIT ? OFFSET ?`, q.Group, q.Group, q.Search, q.Search, q.Limit, q.Offset)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	result := make([]project.Snapshot, 0, len(ids))
	for _, id := range ids {
		record, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

// Binding returns a local locator or opaque secret reference, never a plaintext secret.
func (s *Store) Binding(ctx context.Context, id, key string) (project.Binding, error) {
	var result project.Binding
	err := s.db.QueryRowContext(ctx, "SELECT binding_key,kind,local_locator,secret_ref FROM bindings WHERE workspace_id=? AND project_id=? AND binding_key=?", s.info.WorkspaceID, id, key).Scan(&result.Key, &result.Kind, &result.LocalLocator, &result.SecretRef)
	if errors.Is(err, sql.ErrNoRows) {
		return result, problem.New("BINDING_MISSING", key)
	}
	return result, err
}
