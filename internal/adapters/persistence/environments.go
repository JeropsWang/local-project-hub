package persistence

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

var environmentBindingKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// BindRuntime records only manager-installed SDKs under this workspace.
func (s *Store) BindRuntime(ctx context.Context, projectID, key string, location ports.RuntimeLocation) error {
	if !environmentBindingKey.MatchString(key) {
		return problem.New("BINDING_INVALID", "")
	}
	relative, err := filepath.Rel(filepath.Join(s.dir.Path, "runtimes"), location.Root)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return problem.New("RUNTIME_BINDING_OUTSIDE_WORKSPACE", "")
	}
	executableRelative, err := filepath.Rel(location.Root, location.Executable)
	if err != nil || executableRelative == ".." || strings.HasPrefix(executableRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(executableRelative) {
		return problem.New("RUNTIME_BINDING_OUTSIDE_WORKSPACE", "")
	}
	info, err := os.Lstat(location.Executable)
	if err != nil || !info.Mode().IsRegular() {
		return problem.New("RUNTIME_EXECUTABLE_MISSING", "")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects WHERE project_id=?)", projectID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return problem.New("PROJECT_MISSING", "")
	}
	var conflict bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM bindings WHERE workspace_id=? AND project_id=? AND binding_key=? AND kind<>'runtime')", s.info.WorkspaceID, projectID, key).Scan(&conflict); err != nil {
		return err
	}
	if conflict {
		return problem.New("BINDING_KIND_CONFLICT", key)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO bindings(workspace_id,project_id,binding_key,kind,local_locator,secret_ref) VALUES(?,?,?,'runtime',?,'') ON CONFLICT(workspace_id,project_id,binding_key) DO UPDATE SET local_locator=excluded.local_locator`, s.info.WorkspaceID, projectID, key, location.Executable); err != nil {
		return err
	}
	return tx.Commit()
}
