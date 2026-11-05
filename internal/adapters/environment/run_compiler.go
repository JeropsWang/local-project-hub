package environment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// RunCompiler freezes local commands and reviewed scripts without executing them.
// Runtime bindings identify the selected executable; secrets remain references.
type RunCompiler struct{}

func (RunCompiler) CompileRun(ctx context.Context, req ports.InspectionRequest) (run.Snapshot, error) {
	snapshot := run.Snapshot{
		Document:    req.Document,
		ScriptBytes: map[string][]byte{},
		InputHashes: map[string]string{},
		FileNames:   map[string]string{},
	}
	files := map[string]string{}
	var total int64
	for id, file := range req.Document.Files {
		if err := ctx.Err(); err != nil {
			return snapshot, err
		}
		root, relative := "", ""
		switch file.Availability {
		case "included":
			root = req.WorkspaceRoot
			relative = filepath.ToSlash(
				filepath.Join(filesystem.RevisionPath(req.Document.ProjectID, req.Revision), file.BundlePath),
			)
		case "external", "generated":
			if file.Path == nil {
				return snapshot, problem.New("FILE_PATH_INVALID", id)
			}
			binding, ok := req.Bindings[file.Path.Binding]
			if !ok || binding.Kind != "path" {
				if !file.Required {
					continue
				}
				return snapshot, problem.New("BINDING_MISSING", file.Path.Binding)
			}
			root = binding.LocalLocator
			relative = file.Path.RelativePath
		default:
			return snapshot, problem.New("FILE_AVAILABILITY_UNSUPPORTED", id)
		}
		name, err := resolvedLocalPath(root, relative, false)
		if err != nil {
			if !file.Required {
				continue
			}
			return snapshot, err
		}
		files[id] = name
		if file.Role != "script" {
			continue
		}
		anchor, err := filesystem.OpenLocalRoot(root)
		if err != nil {
			return snapshot, err
		}
		f, err := anchor.Open(relative)
		if err != nil {
			anchor.Close()
			return snapshot, err
		}
		data, err := io.ReadAll(io.LimitReader(f, 16*1024*1024+1))
		f.Close()
		anchor.Close()
		total += int64(len(data))
		if err != nil || len(data) > 16*1024*1024 || total > 128*1024*1024 {
			return snapshot, problem.New("RUN_SCRIPT_LIMIT", id)
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		if file.ExpectedSHA256 != "" && file.ExpectedSHA256 != hash {
			return snapshot, problem.New("FILE_HASH_MISMATCH", id)
		}
		snapshot.ScriptBytes[id] = data
		snapshot.FileNames[id] = filepath.Base(name)
		snapshot.InputHashes[id] = hash
	}
	for _, id := range req.UnitIDs {
		unit, ok := req.Document.Units[id]
		if !ok {
			return snapshot, problem.New("UNIT_MISSING", id)
		}
		frozen := run.UnitPlan{
			ID:         id,
			Kind:       unit.Kind,
			Control:    unit.Control,
			ResourceID: unit.ResourceID,
			Readiness:  unit.Readiness,
		}
		if unit.Kind == "process" || unit.Kind == "task" {
			command := unit.Start
			if unit.Kind == "task" {
				command = unit.Command
			}
			if command == nil {
				return snapshot, problem.New("COMMAND_MISSING", id)
			}
			compiled, err := compileRunCommand(req, *command, files, snapshot.ScriptBytes)
			if err != nil {
				return snapshot, err
			}
			frozen.Start = &compiled
			if unit.Kind == "process" {
				if unit.Stop == nil {
					return snapshot, problem.New("AUTOMATIC_STOP_UNSUPPORTED", id)
				}
				frozen.StopKind = unit.Stop.Kind
				frozen.TimeoutSeconds = unit.Stop.TimeoutSeconds
				if frozen.StopKind == "adapter" {
					if unit.Stop.AdapterID != "managed-control-v1" {
						return snapshot, problem.New("STOP_ADAPTER_UNSUPPORTED", id)
					}
					frozen.StopKind = "managed-pipe"
				}
				if frozen.StopKind != "managed-pipe" && frozen.StopKind != "command" {
					return snapshot, problem.New("AUTOMATIC_STOP_UNSUPPORTED", id)
				}
				if unit.Stop.Command != nil {
					compiled, err := compileRunCommand(req, *unit.Stop.Command, files, snapshot.ScriptBytes)
					if err != nil {
						return snapshot, err
					}
					frozen.Stop = &compiled
				}
			}
		}
		snapshot.Units = append(snapshot.Units, frozen)
	}
	return snapshot, nil
}

func resolvedLocalPath(root, relative string, directory bool) (string, error) {
	if err := project.ValidateRelativePath(relative); err != nil {
		return "", err
	}
	anchor, err := filesystem.OpenLocalRoot(root)
	if err != nil {
		return "", err
	}
	defer anchor.Close()
	current := ""
	for _, part := range strings.Split(relative, "/") {
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		info, err := anchor.Lstat(current)
		if err != nil {
			return "", problem.New("RUN_INPUT_MISSING", relative)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", problem.New("RUN_LINK_UNSUPPORTED", relative)
		}
	}
	info, err := anchor.Lstat(relative)
	if err != nil {
		return "", err
	}
	if directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return "", problem.New("RUN_INPUT_KIND_INVALID", relative)
	}
	return filepath.Join(root, filepath.FromSlash(relative)), nil
}

func compileRunCommand(
	req ports.InspectionRequest,
	command project.Command,
	files map[string]string,
	scripts map[string][]byte,
) (run.Command, error) {
	compiled := run.Command{
		Args:        append([]string{}, command.Args...),
		Environment: map[string]string{},
		SecretRefs:  map[string]string{},
	}
	python := false
	switch command.Executable.Kind {
	case "runtime":
		binding, ok := req.Bindings[command.Executable.RequirementID]
		if !ok || binding.Kind != "runtime" {
			return compiled, problem.New("RUNTIME_BINDING_REQUIRED", command.Executable.RequirementID)
		}
		if !strings.EqualFold(filepath.Base(binding.LocalLocator), filepath.Base(command.Executable.RelativePath)) {
			return compiled, problem.New("RUNTIME_EXECUTABLE_MISMATCH", command.Executable.RequirementID)
		}
		name, err := resolvedLocalPath(filepath.Dir(binding.LocalLocator), filepath.Base(binding.LocalLocator), false)
		if err != nil {
			return compiled, err
		}
		compiled.Executable = name
		python = req.Document.Requirements[command.Executable.RequirementID].Component == "python"
	case "binding":
		binding, ok := req.Bindings[command.Executable.Binding]
		if !ok || binding.Kind != "path" {
			return compiled, problem.New("BINDING_MISSING", command.Executable.Binding)
		}
		name, err := resolvedLocalPath(binding.LocalLocator, command.Executable.RelativePath, false)
		if err != nil {
			return compiled, err
		}
		compiled.Executable = name
		python = strings.EqualFold(filepath.Base(name), "python.exe")
	case "system":
		tool := command.Executable.Tool
		switch tool {
		case "powershell", "pwsh", "cmd", "docker", "git":
		default:
			return compiled, problem.New("SYSTEM_TOOL_UNSUPPORTED", tool)
		}
		name, err := exec.LookPath(tool)
		if err != nil {
			return compiled, problem.New("SYSTEM_TOOL_MISSING", tool)
		}
		compiled.Executable = name
	default:
		return compiled, problem.New("EXECUTABLE_KIND_UNSUPPORTED", "")
	}
	binding, ok := req.Bindings[command.CWD.Binding]
	if !ok || binding.Kind != "path" {
		return compiled, problem.New("BINDING_MISSING", command.CWD.Binding)
	}
	cwd, err := resolvedLocalPath(binding.LocalLocator, command.CWD.RelativePath, true)
	if err != nil {
		return compiled, err
	}
	compiled.CWD = cwd
	for index, arg := range compiled.Args {
		for id, name := range files {
			if _, frozen := scripts[id]; !frozen {
				arg = strings.ReplaceAll(arg, "${file:"+id+"}", name)
			}
		}
		remaining := arg
		for id := range scripts {
			remaining = strings.ReplaceAll(remaining, "${file:"+id+"}", "")
		}
		if strings.Contains(remaining, "${") {
			return compiled, problem.New("COMMAND_PLACEHOLDER_UNRESOLVED", "")
		}
		compiled.Args[index] = arg
	}
	for name, key := range command.EnvBindings {
		switch strings.ToUpper(name) {
		case "PYTHONDONTWRITEBYTECODE",
			"PYTHONPYCACHEPREFIX",
			"GOCACHE",
			"GOMODCACHE",
			"GOTOOLCHAIN",
			"NPM_CONFIG_CACHE":
			return compiled, problem.New("ENVIRONMENT_VALUE_MANAGED", name)
		}
		binding, ok := req.Bindings[key]
		if !ok || binding.Kind != "secret" || binding.SecretRef == "" {
			return compiled, problem.New("SECRET_BINDING_REQUIRED", key)
		}
		compiled.SecretRefs[name] = binding.SecretRef
	}
	// Isolated Python ignores PYTHON* variables, so -B must also be explicit.
	if python {
		compiled.Args = append([]string{"-B"}, compiled.Args...)
	}
	cache := filepath.Join(req.WorkspaceRoot, "cache", "projects", req.Document.ProjectID)
	compiled.Environment["PYTHONDONTWRITEBYTECODE"] = "1"
	compiled.Environment["PYTHONPYCACHEPREFIX"] = filepath.Join(cache, "pyc")
	compiled.Environment["GOCACHE"] = filepath.Join(cache, "go-build")
	compiled.Environment["GOMODCACHE"] = filepath.Join(cache, "go-mod")
	compiled.Environment["GOTOOLCHAIN"] = "local"
	compiled.Environment["npm_config_cache"] = filepath.Join(cache, "npm")
	compiled.Environment["PATH"] = filepath.Dir(compiled.Executable) + string(os.PathListSeparator) + os.Getenv("PATH")
	return compiled, nil
}
