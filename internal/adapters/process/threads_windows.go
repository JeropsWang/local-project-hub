package process

import (
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"golang.org/x/sys/windows"
	"unsafe"
)

// resumeSuspendedThread is shared by the two distinct Job policies. Assignment
// and any required identity capture finish before the initial thread resumes.
func resumeSuspendedThread(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		return err
	}
	return problem.New("PREPARATION_THREAD_MISSING", "")
}
