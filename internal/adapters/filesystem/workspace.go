// Package filesystem confines manager-owned workspace files to a selected root.
package filesystem

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/workspace"
)

// Directory anchors filesystem operations and holds the cross-process lock.
type Directory struct {
	Root *os.Root
	Path string
	lock *os.File
}

// AcquireWorkspace validates a local directory before opening storage.
func AcquireWorkspace(root string, initialize bool) (*Directory, error) {
	if err := checkLocalRoot(root); err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	if initialize {
		if err := os.MkdirAll(root, 0700); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		if len(entries) != 0 {
			return nil, problem.New("WORKSPACE_NOT_EMPTY", "")
		}
	} else if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, problem.New("WORKSPACE_NOT_FOUND", "")
	}
	lock, err := acquireLock(root)
	if err != nil {
		return nil, err
	}
	anchor, err := os.OpenRoot(root)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	return &Directory{Root: anchor, Path: root, lock: lock}, nil
}

// Close releases the anchor and lock; it never removes project files.
func (d *Directory) Close() error {
	return errorsJoin(d.Root.Close(), d.lock.Close())
}

func errorsJoin(first, second error) error {
	if first != nil {
		return first
	}
	return second
}

// NewIdentity generates only workspace identity; no source path is serialized.
func NewIdentity(name string) (workspace.Document, error) {
	if strings.TrimSpace(name) == "" || len([]rune(name)) > 120 {
		return workspace.Document{}, problem.New("WORKSPACE_NAME_INVALID", "")
	}
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return workspace.Document{}, err
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", data[:4], data[4:6], data[6:8], data[8:10], data[10:])
	return workspace.Document{SchemaVersion: "1.0", WorkspaceID: id, Name: name, CreatedAt: time.Now().UTC().Truncate(time.Second)}, nil
}

// WriteIdentity publishes the identity file without replacing any existing content.
func (d *Directory) WriteIdentity(doc workspace.Document) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	file, err := d.Root.OpenFile("workspace.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	return errorsJoin(writeErr, errorsJoin(syncErr, closeErr))
}

// ReadIdentity strictly validates the small manager-owned descriptor.
func (d *Directory) ReadIdentity() (workspace.Document, error) {
	var doc workspace.Document
	if info, err := d.Root.Lstat("workspace.json"); err != nil || !info.Mode().IsRegular() {
		return doc, problem.New("WORKSPACE_IDENTITY_INVALID", "")
	}
	file, err := d.Root.Open("workspace.json")
	if err != nil {
		return doc, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil {
		return doc, err
	}
	if len(data) > 1024*1024 {
		return doc, problem.New("WORKSPACE_IDENTITY_INVALID", "")
	}
	return contract.DecodeWorkspace(data)
}
