package process

import (
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

func processIdentity(handle windows.Handle, pid uint32, controlID string) (run.Identity, error) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return run.Identity{}, err
	}
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return run.Identity{}, err
	}
	return run.Identity{Kind: "process", PID: pid, CreationTime: strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10), Executable: windows.UTF16ToString(buffer[:size]), ControlID: controlID}, nil
}

// openOriginalProcess retains a handle to the verified instance, avoiding a PID-only
// check followed by opening a possibly different process for the actual operation.
func openOriginalProcess(instance run.Identity) (windows.Handle, error) {
	if instance.Kind != "process" || instance.PID == 0 || instance.CreationTime == "" || !filepath.IsAbs(instance.Executable) {
		return 0, problem.New("INSTANCE_IDENTITY_INVALID", "")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, instance.PID)
	if err != nil {
		return 0, problem.New("INSTANCE_NOT_FOUND", "")
	}
	actual, err := processIdentity(handle, instance.PID, instance.ControlID)
	if err != nil || actual.CreationTime != instance.CreationTime || !strings.EqualFold(filepath.Clean(actual.Executable), filepath.Clean(instance.Executable)) {
		windows.CloseHandle(handle)
		return 0, problem.New("INSTANCE_IDENTITY_MISMATCH", "")
	}
	return handle, nil
}
