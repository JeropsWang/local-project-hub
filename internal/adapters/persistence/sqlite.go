// Package persistence implements local manager storage with explicit transactions.
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	_ "modernc.org/sqlite"

	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/workspace"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type factory struct{}

// NewFactory creates locked repositories without changing global machine settings.
func NewFactory() ports.WorkspaceFactory { return factory{} }

// Store serializes configuration writes and owns the SQLite connection and file lock.
type Store struct {
	ownerVerified atomic.Bool
	mu            sync.Mutex
	db            *sql.DB
	dir           *filesystem.Directory
	info          workspace.View
}

// Info returns local workspace state; it contains no project secrets.
func (s *Store) Info() workspace.View { s.mu.Lock(); defer s.mu.Unlock(); return s.info }

// Close flushes manager state and releases the cross-process lock.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.db.Close(), s.dir.Close())
}

func (f factory) Initialize(ctx context.Context, root, name string) (ports.Repository, error) {
	identity, err := filesystem.NewIdentity(name)
	if err != nil {
		return nil, err
	}
	dir, err := filesystem.AcquireWorkspace(root, true)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = dir.Close()
		}
	}()
	file, err := dir.Root.OpenFile("workspace.db", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	db, err := connect(filepath.Join(dir.Path, "workspace.db"))
	if err != nil {
		return nil, err
	}
	defer func() {
		if !success {
			_ = db.Close()
		}
	}()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schemaCurrent); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO workspace_meta VALUES(?,?,?)", identity.WorkspaceID, currentDBVersion, dir.Path); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := dir.WriteIdentity(identity); err != nil {
		return nil, err
	}
	if err := dir.WriteLocalOwner(identity.WorkspaceID); err != nil {
		return nil, err
	}
	success = true
	store := &Store{
		db:  db,
		dir: dir,
		info: workspace.View{
			WorkspaceID:     identity.WorkspaceID,
			Name:            identity.Name,
			Root:            dir.Path,
			State:           "ready",
			DBSchemaVersion: currentDBVersion,
		},
	}
	store.ownerVerified.Store(true)
	return store, nil
}

func (f factory) Open(ctx context.Context, root string) (ports.Repository, error) {
	dir, err := filesystem.AcquireWorkspace(root, false)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = dir.Close()
		}
	}()
	identity, err := dir.ReadIdentity()
	if err != nil {
		return nil, err
	}
	info, err := dir.Root.Lstat("workspace.db")
	if err != nil || !info.Mode().IsRegular() {
		return nil, problem.New("WORKSPACE_DATABASE_MISSING", "")
	}
	db, err := connect(filepath.Join(dir.Path, "workspace.db"))
	if err != nil {
		return nil, err
	}
	defer func() {
		if !success {
			_ = db.Close()
		}
	}()
	var id, lastRoot string
	var version int
	if err := db.QueryRowContext(ctx, "SELECT workspace_id,db_schema_version,last_bound_root FROM workspace_meta").Scan(&id, &version, &lastRoot); err != nil {
		return nil, problem.New("WORKSPACE_DATABASE_INVALID", "")
	}
	if id != identity.WorkspaceID {
		return nil, problem.New("WORKSPACE_IDENTITY_MISMATCH", "")
	}
	if err := upgradeDatabase(ctx, db, version); err != nil {
		return nil, err
	}
	version = currentDBVersion
	state := "ready"
	ownerVerified, err := dir.LocalOwnerMatches(id)
	if err != nil {
		return nil, err
	}
	if !ownerVerified || !strings.EqualFold(filepath.Clean(lastRoot), dir.Path) {
		state = "rebind-required"
	}
	store := &Store{
		db:  db,
		dir: dir,
		info: workspace.View{
			WorkspaceID:     id,
			Name:            identity.Name,
			Root:            dir.Path,
			State:           state,
			DBSchemaVersion: version,
		},
	}
	store.ownerVerified.Store(ownerVerified)
	if err := store.recoverRevisions(ctx); err != nil {
		return nil, err
	}
	if ownerVerified {
		if err := store.interruptRuns(ctx); err != nil {
			return nil, err
		}
	}
	success = true
	return store, nil
}

func connect(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout=3000"); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// HasActive prevents workspace switches from detaching managed runs or mutating tasks.
func (s *Store) HasActive(ctx context.Context) (bool, error) {
	// Unverified origin rows are evidence about another owner, not acquisitions
	// on this machine. They remain visible and untrusted until explicit rebind.
	if !s.ownerVerified.Load() {
		return false, nil
	}
	var active bool
	err := s.db.QueryRowContext(ctx, activeStateQuery).Scan(&active)
	return active, err
}

// ConfirmRelocation resets host-specific bindings and plans in one transaction.
func (s *Store) ConfirmRelocation(ctx context.Context, id string) (workspace.View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.info.WorkspaceID {
		return workspace.View{}, problem.New("WORKSPACE_IDENTITY_MISMATCH", "")
	}
	active, err := s.HasActive(ctx)
	if err != nil {
		return workspace.View{}, err
	}
	if active {
		return workspace.View{}, problem.New("WORKSPACE_BUSY", "")
	}
	if s.info.State != "rebind-required" {
		return s.info, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workspace.View{}, err
	}
	defer tx.Rollback()
	if !s.ownerVerified.Load() {
		if err := detachOriginState(ctx, tx); err != nil {
			return workspace.View{}, err
		}
	}
	for _, statement := range []string{"DELETE FROM bindings", "DELETE FROM resources", "UPDATE plans SET state='invalidated'"} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return workspace.View{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE workspace_meta SET last_bound_root=? WHERE workspace_id=?", s.info.Root, id); err != nil {
		return workspace.View{}, err
	}
	if err := tx.Commit(); err != nil {
		return workspace.View{}, err
	}
	if err := s.dir.WriteLocalOwner(id); err != nil {
		return workspace.View{}, err
	}
	s.ownerVerified.Store(true)
	s.info.State = "ready"
	return s.info, nil
}
