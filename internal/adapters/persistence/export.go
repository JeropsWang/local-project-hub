package persistence

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"path/filepath"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

// ExportRevision reads an immutable revision even after a newer one is saved.
func (s *Store) ExportRevision(ctx context.Context, ref project.Ref) (bundle.ExportInput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var input bundle.ExportInput
	var expected string
	err := s.db.QueryRowContext(ctx, "SELECT document_json,content_hash FROM project_revisions WHERE project_id=? AND revision=?", ref.ProjectID, ref.ExpectedRevision).
		Scan(&input.Project, &expected)
	if errors.Is(err, sql.ErrNoRows) {
		return input, problem.New("REVISION_MISSING", "")
	}
	if err != nil {
		return input, err
	}
	if bundle.Digest(input.Project) != expected {
		return input, problem.New("REVISION_FILE_CHANGED", bundle.ProjectFile)
	}
	doc, err := contract.DecodeProject(input.Project)
	if err != nil {
		return input, err
	}
	if doc.ProjectID != string(ref.ProjectID) {
		return input, problem.New("REVISION_FILE_CHANGED", bundle.ProjectFile)
	}
	if _, ok := doc.Profiles[string(ref.ProfileID)]; !ok {
		return input, problem.New("PROFILE_MISSING", "")
	}
	closure, err := bundle.Collect(doc, func(name string) ([]byte, error) {
		return s.readRevisionFile(ctx, ref.ProjectID, ref.ExpectedRevision, name, bundle.MaxFile)
	})
	if err != nil {
		return bundle.ExportInput{}, err
	}
	input.Files = closure.Files
	return input, nil
}

// ReadRevisionFile loads only the requested committed file, without opening
// any externally bound path or reading the rest of the revision's inventory.
func (s *Store) ReadRevisionFile(ctx context.Context, id project.ID, revision int32, name string, limit int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readRevisionFile(ctx, id, revision, name, limit)
}

func (s *Store) readRevisionFile(ctx context.Context, id project.ID, revision int32, name string, limit int64) ([]byte, error) {
	if err := bundle.ValidateFilePath(name); err != nil {
		return nil, err
	}
	var expected string
	var size int64
	err := s.db.QueryRowContext(ctx, "SELECT sha256,size FROM revision_files WHERE project_id=? AND revision=? AND relative_path=?", id, revision, name).
		Scan(&expected, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, problem.New("BUNDLE_FILE_MISSING", name)
	}
	if err != nil {
		return nil, err
	}
	if size > bundle.MaxFile || size < 0 {
		return nil, problem.New("REVISION_FILE_CHANGED", name)
	}
	if size > limit {
		return nil, problem.New("REVISION_FILE_LIMIT", name)
	}
	full := filepath.Join(
		s.dir.Path,
		filesystem.RevisionPath(string(id), revision),
		filepath.FromSlash(name),
	)
	parent, err := filesystem.OpenLocalRoot(filepath.Dir(full))
	if err != nil {
		return nil, problem.New("BUNDLE_FILE_MISSING", name)
	}
	defer parent.Close()
	info, err := parent.Lstat(filepath.Base(full))
	if err != nil {
		return nil, problem.New("BUNDLE_FILE_MISSING", name)
	}
	if !info.Mode().IsRegular() || size != info.Size() {
		return nil, problem.New("REVISION_FILE_CHANGED", name)
	}
	file, err := parent.Open(filepath.Base(full))
	if err != nil {
		return nil, problem.New("BUNDLE_FILE_MISSING", name)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != size || bundle.Digest(data) != expected {
		return nil, problem.New("REVISION_FILE_CHANGED", name)
	}
	return data, nil
}
