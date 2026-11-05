package filesystem

import (
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"sort"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

const revisionOwnerFile = ".lph-revision-owner"

// RevisionPath is computed from validated identity, never an external locator.
func RevisionPath(id string, revision int32) string {
	return fmt.Sprintf("projects/%s/revisions/%d", id, revision)
}

// RevisionPublication proves which staging directory this call actually created.
type RevisionPublication struct {
	stage   string
	created bool
}

// WriteRevision publishes an immutable directory with a journal-matching ownership marker.
// A conflicting destination is never adopted or deleted.
func (d *Directory) WriteRevision(doc project.Document, revision int32, owner string, provided map[string][]byte) (RevisionPublication, error) {
	result := RevisionPublication{}
	if !validOwner(owner) {
		return result, problem.New("REVISION_OWNER_INVALID", "")
	}
	final := RevisionPath(doc.ProjectID, revision)
	if err := project.ValidateRelativePath(final); err != nil {
		return result, err
	}
	if _, err := d.Root.Lstat(final); !os.IsNotExist(err) {
		return result, problem.New("REVISION_FILE_CONFLICT", "")
	}
	stage := final + ".staging-" + owner
	if err := d.Root.MkdirAll(path.Dir(stage), 0700); err != nil {
		return result, err
	}
	if err := d.Root.Mkdir(stage, 0700); err != nil {
		return result, problem.New("REVISION_FILE_CONFLICT", "")
	}
	result.stage = stage
	result.created = true
	if err := writeSynced(d.Root, path.Join(stage, revisionOwnerFile), []byte(owner)); err != nil {
		return result, err
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
			return result, problem.New("REVISION_UNDECLARED_FILE", "")
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := project.ValidateRelativePath(key); err != nil {
			return result, err
		}
		data, ok := provided[key]
		if !ok {
			return result, problem.New("BUNDLE_FILE_MISSING", key)
		}
		if len(data) > 16*1024*1024 {
			return result, problem.New("REVISION_FILE_LIMIT", key)
		}
		dest := path.Join(stage, key)
		if err := d.Root.MkdirAll(path.Dir(dest), 0700); err != nil {
			return result, err
		}
		if err := writeSynced(d.Root, dest, data); err != nil {
			return result, err
		}
	}
	if err := d.Root.Rename(stage, final); err != nil {
		return result, err
	}
	result.created = false
	return result, nil
}

func writeSynced(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errorsJoin(writeErr, errorsJoin(syncErr, closeErr))
}

// DiscardPublication removes only a staging directory actually created by this call.
func (d *Directory) DiscardPublication(publication RevisionPublication) error {
	if !publication.created {
		return nil
	}
	if err := project.ValidateRelativePath(publication.stage); err != nil {
		return err
	}
	return d.Root.RemoveAll(publication.stage)
}

// RemoveOwnedRevision requires the journal marker before cleanup. Unknown content is preserved.
func (d *Directory) RemoveOwnedRevision(id string, revision int32, owner string) error {
	if !validOwner(owner) {
		return problem.New("RECOVERY_JOURNAL_INVALID", "")
	}
	final := RevisionPath(id, revision)
	if err := project.ValidateRelativePath(final); err != nil {
		return err
	}
	for _, location := range []string{final + ".staging-" + owner, final} {
		if _, err := d.Root.Lstat(location); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		marker, err := d.Root.ReadFile(path.Join(location, revisionOwnerFile))
		if err != nil || string(marker) != owner {
			return problem.New("WORKSPACE_RECOVERY_REQUIRED", id)
		}
		if err := d.Root.RemoveAll(location); err != nil {
			return err
		}
	}
	return nil
}

func validOwner(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
