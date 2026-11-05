package process

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/control"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

var processKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// NativeProcessAdapter holds handles to original instances and their non-killing
// Jobs. Its authority comes from these acquisitions, never a process name or PID scan.
type NativeProcessAdapter struct {
	mu        sync.RWMutex
	workspace string
	root      *os.Root
	instances map[string]*nativeInstance
	closed    bool
}

type nativeInstance struct {
	identity      run.Identity
	process       windows.Handle
	job           windows.Handle
	tracker       *jobTracker
	log           *RunLog
	stop          ports.StopSpec
	scripts       map[string]string
	token         string
	mu            sync.Mutex
	operationID   string
	operationDone chan struct{}
	controlResult ControlOutcome
	controlError  error
	controlCancel context.CancelFunc
	waitDone      chan struct{}
}

// NewProcessAdapter requires an initialized local workspace; it starts nothing.
func NewProcessAdapter(workspace string) (*NativeProcessAdapter, error) {
	root, err := filesystem.OpenLocalRoot(workspace)
	if err != nil {
		return nil, err
	}
	file, err := root.Open("workspace.json")
	if err != nil {
		root.Close()
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	file.Close()
	if err != nil {
		root.Close()
		return nil, err
	}
	if _, err := contract.DecodeWorkspace(data); err != nil {
		root.Close()
		return nil, err
	}
	return &NativeProcessAdapter{workspace: workspace, root: root, instances: map[string]*nativeInstance{}}, nil
}

// Start captures scripts and stop argv before creating a suspended process. The
// process joins its Job and gets protected credentials before any project code runs.
func (a *NativeProcessAdapter) Start(ctx context.Context, spec ports.ProcessSpec) (run.Identity, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return run.Identity{}, problem.New("PROCESS_ADAPTER_CLOSED", "")
	}
	if err := ctx.Err(); err != nil {
		return run.Identity{}, err
	}
	if !control.ValidIdentity(spec.RunID) || !processKey.MatchString(spec.UnitID) {
		return run.Identity{}, problem.New("PROCESS_SPEC_INVALID", "")
	}
	if _, exists := a.instances[spec.RunID]; exists {
		return run.Identity{}, problem.New("PROCESS_ALREADY_STARTED", "")
	}
	if spec.Stop.Kind != "managed-pipe" && (spec.Stop.Kind != "command" || spec.Stop.Command == nil) {
		return run.Identity{}, problem.New("AUTOMATIC_STOP_UNSUPPORTED", "")
	}
	if err := validateProcessCommand(spec.Command); err != nil {
		return run.Identity{}, err
	}
	if spec.Stop.Command != nil {
		if err := validateProcessCommand(*spec.Stop.Command); err != nil {
			return run.Identity{}, err
		}
	}
	if err := a.root.MkdirAll("runs", 0700); err != nil {
		return run.Identity{}, err
	}
	relative := path.Join("runs", spec.RunID)
	if err := a.root.Mkdir(relative, 0700); err != nil {
		return run.Identity{}, problem.New("RUN_SNAPSHOT_EXISTS", "")
	}
	started := false
	defer func() {
		if !started {
			_ = a.root.RemoveAll(relative)
		}
	}()
	scripts, err := a.snapshotScripts(relative, spec.Scripts)
	if err != nil {
		return run.Identity{}, err
	}
	launch, err := snapshotCommand(spec.Command, scripts)
	if err != nil {
		return run.Identity{}, err
	}
	stop := spec.Stop
	if stop.Command != nil {
		fixed, err := snapshotCommand(*stop.Command, scripts)
		if err != nil {
			return run.Identity{}, err
		}
		stop.Command = &fixed
	}
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return run.Identity{}, err
	}
	token := hex.EncodeToString(bytes[:])
	log, err := NewRunLog(a.workspace, spec.RunID, append(slices.Clone(spec.SecretValues), token))
	if err != nil {
		return run.Identity{}, err
	}
	job, tracker, err := newBusinessJob()
	if err != nil {
		log.Close()
		return run.Identity{}, err
	}
	stdout, stderr := log.Writer("stdout"), log.Writer("stderr")
	command := exec.Command(launch.Executable, launch.Args...)
	command.Dir = launch.CWD
	command.Env = businessEnvironment(launch.Environment)
	command.Env = append(
		command.Env,
		"LPH_RUN_ID="+spec.RunID,
		"LPH_CONTROL_PIPE="+`\\.\pipe\LocalProjectHub.`+spec.RunID,
		"LPH_CONTROL_TOKEN="+token,
	)
	command.Stdout, command.Stderr = stdout, stderr
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW,
	}
	if err := command.Start(); err != nil {
		tracker.close()
		windows.CloseHandle(job)
		log.Close()
		return run.Identity{}, err
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE,
		false,
		uint32(command.Process.Pid),
	)
	if err == nil {
		err = windows.AssignProcessToJobObject(job, handle)
	}
	var instanceID run.Identity
	if err == nil {
		instanceID, err = processIdentity(handle, uint32(command.Process.Pid), spec.RunID)
	}
	if err == nil {
		err = saveControl(a.root, instanceID, token)
	}
	if err == nil {
		err = resumeSuspendedThread(uint32(command.Process.Pid))
	}
	if err != nil {
		// A suspended process has executed no business code. This is launch cleanup,
		// never a timeout fallback for a running/saving project.
		_ = command.Process.Kill()
		_ = command.Wait()
		if handle != 0 {
			windows.CloseHandle(handle)
		}
		windows.CloseHandle(job)
		tracker.close()
		log.Close()
		return run.Identity{}, problem.New("PROCESS_LAUNCH_FAILED", "")
	}
	hashes := map[string]string{}
	for key, name := range scripts {
		hashes[name] = spec.Scripts[key].SHA256
	}
	instance := &nativeInstance{
		identity: instanceID,
		process:  handle,
		job:      job,
		tracker:  tracker,
		log:      log,
		stop:     stop,
		scripts:  hashes,
		token:    token,
		waitDone: make(chan struct{}),
	}
	a.instances[spec.RunID] = instance
	go func() { _ = command.Wait(); _ = stdout.Close(); _ = stderr.Close(); close(instance.waitDone) }()
	started = true
	return instanceID, nil
}

// Inspect checks the retained original process handle and the whole Job. Parent
// shell exit cannot erase live descendants or authorize a replacement PID.
func (a *NativeProcessAdapter) Inspect(ctx context.Context, identity run.Identity) (ports.InstanceState, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return ports.InstanceState{}, err
	}
	instance, err := a.original(identity)
	if err != nil {
		return ports.InstanceState{}, err
	}
	return inspectNative(instance)
}

func (a *NativeProcessAdapter) original(identity run.Identity) (*nativeInstance, error) {
	instance, ok := a.instances[identity.ControlID]
	if !ok {
		return nil, problem.New("INSTANCE_NOT_OWNED", "")
	}
	if identity != instance.identity {
		return nil, problem.New("INSTANCE_IDENTITY_MISMATCH", "")
	}
	if _, err := windows.WaitForSingleObject(instance.process, 0); err != nil {
		return nil, err
	}
	// Start captured PID, creation time and actual image while suspended. The
	// complete caller identity matched that immutable acquisition above, and this
	// handle cannot change process objects while held under a.mu. Image queries
	// can fail during exit cleanup before the process object becomes signaled;
	// repeating that query introduces a false mismatch, not stronger authority.
	// Never replace this handle with OpenProcess on a reusable PID.
	return instance, nil
}

func inspectNative(instance *nativeInstance) (ports.InstanceState, error) {
	var state ports.InstanceState
	status, err := windows.WaitForSingleObject(instance.process, 0)
	if err != nil {
		return state, err
	}
	state.RootRunning = status == uint32(windows.WAIT_TIMEOUT)
	var total uint32
	state.ActiveProcesses, total, err = businessJobProcesses(instance.job)
	if err != nil {
		return state, err
	}
	if !state.RootRunning {
		if err := windows.GetExitCodeProcess(instance.process, &state.ExitCode); err != nil {
			return state, err
		}
	}
	state.Exited = !state.RootRunning && state.ActiveProcesses == 0
	state.ExitCodesVerified, state.ExitFailure, state.ExitVerificationFailed = instance.tracker.exitStatus(total)
	return state, nil
}

// Close refuses active process trees. Closing an empty adapter never kills a project.
func (a *NativeProcessAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	for _, instance := range a.instances {
		state, err := inspectNative(instance)
		if err != nil {
			return err
		}
		if !state.Exited {
			return problem.New("PROCESS_STILL_RUNNING", "")
		}
	}
	for _, instance := range a.instances {
		if instance.controlCancel != nil {
			instance.controlCancel()
		}
		<-instance.waitDone
		_ = instance.log.Close()
		instance.tracker.close()
		windows.CloseHandle(instance.process)
		windows.CloseHandle(instance.job)
		instance.token = ""
	}
	a.closed = true
	return a.root.Close()
}

func validateProcessCommand(command ports.ProcessCommand) error {
	if !filepath.IsAbs(command.Executable) || strings.IndexByte(command.Executable, 0) >= 0 {
		return problem.New("PROCESS_EXECUTABLE_INVALID", "")
	}
	parent, err := filesystem.OpenLocalRoot(filepath.Dir(command.Executable))
	if err != nil {
		return err
	}
	info, err := parent.Lstat(filepath.Base(command.Executable))
	parent.Close()
	if err != nil || !info.Mode().IsRegular() {
		return problem.New("PROCESS_EXECUTABLE_MISSING", "")
	}
	root, err := filesystem.OpenLocalRoot(command.CWD)
	if err != nil {
		return err
	}
	root.Close()
	for _, arg := range command.Args {
		if strings.IndexByte(arg, 0) >= 0 {
			return problem.New("PROCESS_ARGUMENT_INVALID", "")
		}
	}
	for name, value := range command.Environment {
		if strings.ContainsAny(name, "=\x00") || name == "" || strings.IndexByte(value, 0) >= 0 ||
			strings.HasPrefix(strings.ToUpper(name), "LPH_") {
			return problem.New("PROCESS_ENVIRONMENT_INVALID", "")
		}
	}
	return nil
}

func (a *NativeProcessAdapter) snapshotScripts(
	relative string,
	scripts map[string]ports.ProcessScript,
) (map[string]string, error) {
	if len(scripts) > 2048 {
		return nil, problem.New("RUN_SNAPSHOT_LIMIT", "")
	}
	if err := a.root.MkdirAll(path.Join(relative, "scripts"), 0700); err != nil {
		return nil, err
	}
	result := map[string]string{}
	var total int64
	for key, script := range scripts {
		total += int64(len(script.Bytes))
		digest := sha256.Sum256(script.Bytes)
		if !processKey.MatchString(key) || len(script.Bytes) > 16*1024*1024 || total > 128*1024*1024 ||
			hex.EncodeToString(digest[:]) != script.SHA256 {
			return nil, problem.New("RUN_SCRIPT_INVALID", key)
		}
		name := path.Join(relative, "scripts", key)
		if script.Name != "" {
			if project.ValidateRelativePath(script.Name) != nil || path.Base(script.Name) != script.Name ||
				script.Name == "." {
				return nil, problem.New("RUN_SCRIPT_INVALID", key)
			}
			if err := a.root.Mkdir(name, 0700); err != nil {
				return nil, err
			}
			name = path.Join(name, script.Name)
		}
		file, err := a.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		_, writeErr := file.Write(script.Bytes)
		syncErr, closeErr := file.Sync(), file.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if syncErr != nil {
			return nil, syncErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		result[key] = filepath.Join(a.workspace, filepath.FromSlash(name))
	}
	return result, nil
}

func snapshotCommand(command ports.ProcessCommand, scripts map[string]string) (ports.ProcessCommand, error) {
	command.Args = slices.Clone(command.Args)
	command.Environment = cloneEnvironment(command.Environment)
	for i, arg := range command.Args {
		for key, file := range scripts {
			arg = strings.ReplaceAll(arg, "${file:"+key+"}", file)
		}
		if strings.Contains(arg, "${file:") {
			return command, problem.New("RUN_SCRIPT_MISSING", "")
		}
		command.Args[i] = arg
	}
	return command, nil
}

func cloneEnvironment(values map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range values {
		result[key] = value
	}
	return result
}

func businessEnvironment(values map[string]string) []string {
	result := map[string]string{}
	for _, key := range []string{"SYSTEMROOT", "WINDIR", "COMSPEC", "SYSTEMDRIVE", "PATH", "PATHEXT", "TEMP", "TMP", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE"} {
		if value := os.Getenv(key); value != "" {
			result[key] = value
		}
	}
	for key, value := range values {
		result[strings.ToUpper(key)] = value
	}
	keys := []string{}
	for key := range result {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := []string{}
	for _, key := range keys {
		entries = append(entries, key+"="+result[key])
	}
	return entries
}
