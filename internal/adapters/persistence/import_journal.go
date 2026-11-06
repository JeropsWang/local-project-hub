package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
)

// SaveImportPreview stores only the package locator and its evidence. Project
// publication uses the existing owned revision journal at commit time.
func (s *Store) SaveImportPreview(ctx context.Context, id string, preview bundle.ImportPlan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.State != "ready" {
		return problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	data, err := json.Marshal(preview)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM import_journal WHERE phase='preview' AND json_extract(target_json,'$.ExpiresAt')<=?", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM import_journal WHERE phase='preview'").Scan(&count); err != nil {
		return err
	}
	if count >= 128 {
		return problem.New("IMPORT_PREVIEW_LIMIT", "")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO import_journal VALUES(?,'',?,'preview')", id, data); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LoadImportPreview(ctx context.Context, id string) (bundle.ImportPlan, error) {
	var preview bundle.ImportPlan
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT target_json FROM import_journal WHERE preview_id=? AND phase='preview'", id).
		Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return preview, problem.New("IMPORT_PREVIEW_MISSING", "")
	}
	if err != nil {
		return preview, err
	}
	err = json.Unmarshal(data, &preview)
	return preview, err
}
