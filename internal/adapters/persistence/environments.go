package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/preparation"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

var environmentBindingKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// ActivateEnvironment commits all local selections together after verifying the
// ready receipt and optimistic revision/bindings. A running project is immutable.
func (s *Store) ActivateEnvironment(ctx context.Context, receipt preparation.Receipt) error {
	if receipt.WorkspaceID != s.info.WorkspaceID || receipt.State != "ready" || !environmentContained(filepath.Join(s.dir.Path, "envs"), receipt.Root) || len(receipt.Bindings) == 0 {
		return problem.New("ENVIRONMENT_RECEIPT_INVALID", "")
	}
	anchor, err := filesystem.OpenLocalRoot(receipt.Root)
	if err != nil {
		return err
	}
	defer anchor.Close()
	file, err := anchor.Open(".lph-environment.json")
	if err != nil {
		return problem.New("ENVIRONMENT_RECEIPT_INVALID", "")
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	file.Close()
	var stored preparation.Receipt
	if readErr != nil || len(data) > 1024*1024 || json.Unmarshal(data, &stored) != nil {
		return problem.New("ENVIRONMENT_RECEIPT_INVALID", "")
	}
	want, _ := json.Marshal(receipt)
	have, _ := json.Marshal(stored)
	if string(want) != string(have) {
		return problem.New("ENVIRONMENT_RECEIPT_INVALID", "")
	}
	seen := map[string]bool{}
	for _, binding := range receipt.Bindings {
		if !environmentBindingKey.MatchString(binding.Key) || seen[binding.Key] || binding.SecretRef != "" {
			return problem.New("BINDING_INVALID", "")
		}
		seen[binding.Key] = true
		switch binding.Kind {
		case "path":
			if !strings.EqualFold(filepath.Clean(binding.LocalLocator), filepath.Join(receipt.Root, "sources", binding.Key)) {
				return problem.New("ENVIRONMENT_BINDING_OUTSIDE_WORKSPACE", binding.Key)
			}
			root, err := filesystem.OpenLocalRoot(binding.LocalLocator)
			if err != nil {
				return err
			}
			root.Close()
		case "runtime":
			if !environmentContained(receipt.Root, binding.LocalLocator) && !environmentContained(filepath.Join(s.dir.Path, "runtimes"), binding.LocalLocator) {
				return problem.New("ENVIRONMENT_BINDING_OUTSIDE_WORKSPACE", binding.Key)
			}
			parent, err := filesystem.OpenLocalRoot(filepath.Dir(binding.LocalLocator))
			if err != nil {
				return err
			}
			info, err := parent.Lstat(filepath.Base(binding.LocalLocator))
			parent.Close()
			if err != nil || !info.Mode().IsRegular() {
				return problem.New("RUNTIME_EXECUTABLE_MISSING", binding.Key)
			}
		default:
			return problem.New("BINDING_INVALID", binding.Key)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision int32
	if err := tx.QueryRowContext(ctx, "SELECT current_revision FROM projects WHERE project_id=?", receipt.Ref.ProjectID).Scan(&revision); err != nil {
		return err
	}
	if revision != receipt.Ref.ExpectedRevision {
		return problem.New("REVISION_CONFLICT", "")
	}
	var running bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM runs WHERE project_id=? AND state NOT IN ('stopped','failed-start'))", receipt.Ref.ProjectID).Scan(&running); err != nil {
		return err
	}
	if running {
		return problem.New("PROJECT_RUNNING", "")
	}
	originals := map[string]project.Binding{}
	for _, binding := range receipt.OriginalBindings {
		if _, exists := originals[binding.Key]; exists {
			return problem.New("BINDING_INVALID", binding.Key)
		}
		originals[binding.Key] = binding
	}
	for _, binding := range receipt.Bindings {
		var current project.Binding
		current.Key = binding.Key
		err := tx.QueryRowContext(ctx, "SELECT kind,local_locator,secret_ref FROM bindings WHERE workspace_id=? AND project_id=? AND binding_key=?", s.info.WorkspaceID, receipt.Ref.ProjectID, binding.Key).Scan(&current.Kind, &current.LocalLocator, &current.SecretRef)
		previous, existed := originals[binding.Key]
		if existed && (err != nil || current != previous) || !existed && !errors.Is(err, sql.ErrNoRows) {
			return problem.New("ENVIRONMENT_BINDING_CONFLICT", binding.Key)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO bindings VALUES(?,?,?,?,?,'') ON CONFLICT(workspace_id,project_id,binding_key) DO UPDATE SET kind=excluded.kind,local_locator=excluded.local_locator,secret_ref=''`, s.info.WorkspaceID, receipt.Ref.ProjectID, binding.Key, binding.Kind, binding.LocalLocator); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func environmentContained(root, candidate string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(candidate) {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

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
