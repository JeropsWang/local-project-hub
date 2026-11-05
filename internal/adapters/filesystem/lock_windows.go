package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"golang.org/x/sys/windows"
)

// checkLocalRoot excludes UNC, mapped network drives and reparse point ancestors.
func checkLocalRoot(root string) error {
	if strings.HasPrefix(root, `\\`) || !filepath.IsAbs(root) {
		return problem.New("WORKSPACE_PATH_UNSUPPORTED", "")
	}
	volume := filepath.VolumeName(root) + `\`
	ptr, err := windows.UTF16PtrFromString(volume)
	if err != nil {
		return problem.New("WORKSPACE_PATH_UNSUPPORTED", "")
	}
	if kind := windows.GetDriveType(ptr); kind != windows.DRIVE_FIXED && kind != windows.DRIVE_REMOVABLE {
		return problem.New("WORKSPACE_PATH_UNSUPPORTED", "")
	}
	for current := root; ; current = filepath.Dir(current) {
		ptr, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return problem.New("WORKSPACE_PATH_UNSUPPORTED", "")
		}
		attrs, err := windows.GetFileAttributes(ptr)
		if err == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return problem.New("WORKSPACE_REPARSE_POINT", "")
		}
		if err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return err
		}
		if parent := filepath.Dir(current); parent == current {
			break
		}
	}
	return nil
}

// acquireLock uses a non-shared Windows handle; process death releases the lock.
func acquireLock(root string) (*os.File, error) {
	ptr, err := windows.UTF16PtrFromString(filepath.Join(root, ".workspace.lock"))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(ptr, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return nil, problem.New("WORKSPACE_BUSY", "")
	}
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), ".workspace.lock"), nil
}
