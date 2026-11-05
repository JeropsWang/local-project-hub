package filesystem

import (
	"fmt"
	"os"
	"path"
	"sort"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

// RevisionPath is computed from validated internal identity, never a stored external locator.
func RevisionPath(id string, revision int32) string {
	return fmt.Sprintf("projects/%s/revisions/%d", id, revision)
}

// WriteRevision publishes an immutable owned directory. Every included resource is
// required to exist; omitted bytes may be copied from the prior immutable revision.
func (d *Directory) WriteRevision(doc project.Document, revision int32, previous int32, provided map[string][]byte) error {
	final := RevisionPath(doc.ProjectID, revision)
	stage := final + ".staging"
	if _, err := d.Root.Lstat(final); !os.IsNotExist(err) {
		return problem.New("REVISION_FILE_CONFLICT", "")
	}
	if _, err := d.Root.Lstat(stage); !os.IsNotExist(err) {
		return problem.New("REVISION_FILE_CONFLICT", "")
	}
	if err := d.Root.MkdirAll(stage, 0700); err != nil {
		return err
	}
	keys := []string{}
	allowed := map[string]bool{}
	for _, file := range doc.Files {
		if file.Availability == "included" {
			keys = append(keys, file.BundlePath)
			allowed[file.BundlePath] = true
		}
	}
	for key := range provided {
		if !allowed[key] {
			return problem.New("REVISION_UNDECLARED_FILE", "")
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := project.ValidateRelativePath(key); err != nil {
			return err
		}
		data, ok := provided[key]
		if !ok && previous > 0 {
			var err error
			data, err = d.Root.ReadFile(path.Join(RevisionPath(doc.ProjectID, previous), key))
			if err != nil {
				return problem.New("BUNDLE_FILE_MISSING", key)
			}
			ok = true
		}
		if !ok {
			return problem.New("BUNDLE_FILE_MISSING", key)
		}
		if len(data) > 16*1024*1024 {
			return problem.New("REVISION_FILE_LIMIT", key)
		}
		dest := path.Join(stage, key)
		if err := d.Root.MkdirAll(path.Dir(dest), 0700); err != nil {
			return err
		}
		file, err := d.Root.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(data)
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errorsJoin(writeErr, errorsJoin(syncErr, closeErr)); err != nil {
			return err
		}
	}
	return d.Root.Rename(stage, final)
}

// RemoveRevision cleans only a computed manager-owned location, not a journal-supplied path.
func (d *Directory) RemoveRevision(id string, revision int32) error {
	if err := project.ValidateRelativePath(RevisionPath(id, revision)); err != nil {
		return err
	}
	for _, value := range []string{RevisionPath(id, revision) + ".staging", RevisionPath(id, revision)} {
		if err := d.Root.RemoveAll(value); err != nil {
			return err
		}
	}
	return nil
}
