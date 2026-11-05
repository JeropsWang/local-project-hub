package process

import (
	"errors"
	"strconv"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A business Job intentionally has no KILL_ON_JOB_CLOSE or breakaway flags.
// Parent exit and manager crashes therefore cannot silently kill saving projects.
func newBusinessJob() (windows.Handle, *jobTracker, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, nil, err
	}
	port, err := windows.CreateIoCompletionPort(windows.InvalidHandle, 0, 0, 1)
	if err != nil {
		windows.CloseHandle(job)
		return 0, nil, err
	}
	association := struct {
		Key  uintptr
		Port windows.Handle
	}{Key: 1, Port: port}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectAssociateCompletionPortInformation, uintptr(unsafe.Pointer(&association)), uint32(unsafe.Sizeof(association))); err != nil {
		windows.CloseHandle(port)
		windows.CloseHandle(job)
		return 0, nil, err
	}
	tracker := &jobTracker{job: job, port: port, seen: map[string]bool{}, done: make(chan struct{})}
	go tracker.observe()
	return job, tracker, nil
}

func businessJobProcesses(job windows.Handle) (uint32, uint32, error) {
	var info struct {
		TotalUserTime            int64
		TotalKernelTime          int64
		ThisPeriodUserTime       int64
		ThisPeriodKernelTime     int64
		TotalPageFaultCount      uint32
		TotalProcesses           uint32
		ActiveProcesses          uint32
		TotalTerminatedProcesses uint32
	}
	var returned uint32
	err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), &returned)
	return info.ActiveProcesses, info.TotalProcesses, err
}

type jobTracker struct {
	mu        sync.Mutex
	job       windows.Handle
	port      windows.Handle
	processes []windows.Handle
	seen      map[string]bool
	unknown   bool
	abnormal  bool
	done      chan struct{}
}

var isProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

// A NEW_PROCESS notification is only a PID hint. Hold a handle and verify Job
// membership before trusting it; a missed/too-fast child remains unverified.
func (t *jobTracker) observe() {
	defer close(t.done)
	for {
		var message uint32
		var key uintptr
		var value *windows.Overlapped
		err := windows.GetQueuedCompletionStatus(t.port, &message, &key, &value, 1000)
		if errors.Is(err, windows.WAIT_TIMEOUT) {
			continue
		}
		if err != nil {
			t.mu.Lock()
			t.unknown = true
			t.mu.Unlock()
			return
		}
		if key == 0 {
			return
		}
		if message == 8 {
			t.mu.Lock()
			t.abnormal = true
			t.mu.Unlock()
		}
		if message != 6 {
			continue
		}
		pid := uint32(uintptr(unsafe.Pointer(value)))
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
		if err != nil {
			t.mu.Lock()
			t.unknown = true
			t.mu.Unlock()
			continue
		}
		var belongs int32
		ok, _, _ := isProcessInJob.Call(uintptr(handle), uintptr(t.job), uintptr(unsafe.Pointer(&belongs)))
		var creation, exit, kernel, user windows.Filetime
		timeErr := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user)
		t.mu.Lock()
		if ok == 0 || belongs == 0 || timeErr != nil || len(t.processes) >= 4096 {
			t.unknown = true
			windows.CloseHandle(handle)
			t.mu.Unlock()
			continue
		}
		identity := strconv.FormatUint(uint64(pid), 10) + ":" + strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10)
		if t.seen[identity] {
			windows.CloseHandle(handle)
		} else {
			t.seen[identity] = true
			t.processes = append(t.processes, handle)
		}
		t.mu.Unlock()
	}
}

func (t *jobTracker) exitStatus(total uint32) (verified, failed, unknown bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	unknown = t.unknown
	failed = t.abnormal
	verified = !unknown && uint32(len(t.processes)) == total
	for _, handle := range t.processes {
		status, err := windows.WaitForSingleObject(handle, 0)
		if err != nil {
			unknown = true
			verified = false
			continue
		}
		if status != windows.WAIT_OBJECT_0 {
			verified = false
			continue
		}
		var code uint32
		if err := windows.GetExitCodeProcess(handle, &code); err != nil {
			unknown = true
			verified = false
		} else if code != 0 {
			failed = true
		}
	}
	return verified, failed, unknown
}

func (t *jobTracker) close() {
	_ = windows.PostQueuedCompletionStatus(t.port, 0, 0, nil)
	<-t.done
	for _, handle := range t.processes {
		windows.CloseHandle(handle)
	}
	windows.CloseHandle(t.port)
}
