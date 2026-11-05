package environment

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// inspectInvocations validates targets without invoking the project or system tool.
func inspectInvocations(ctx context.Context, req ports.InspectionRequest) []check.Item {
	result := []check.Item{}
	for _, id := range req.UnitIDs {
		if ctx.Err() != nil {
			return append(result, invocationIssue(id, check.Unknown, "CHECK_BUDGET_EXCEEDED"))
		}
		unit := req.Document.Units[id]
		commands := []*project.Command{unit.Start, unit.Command}
		if unit.Stop != nil {
			commands = append(commands, unit.Stop.Command)
		}
		for _, command := range commands {
			if command == nil {
				continue
			}
			binding, ok := req.Bindings[command.CWD.Binding]
			if !ok || binding.Kind != "path" || !invocationPath(binding.LocalLocator, command.CWD.RelativePath, true) {
				result = append(result, invocationIssue(id+":cwd", check.Missing, "COMMAND_DIRECTORY_MISSING"))
			}
			var root, relative string
			executable := command.Executable
			switch executable.Kind {
			case "binding":
				binding, ok := req.Bindings[executable.Binding]
				if !ok || binding.Kind != "path" {
					result = append(result, invocationIssue(id+":executable", check.BindingRequired, "COMMAND_BINDING_REQUIRED"))
					continue
				}
				root = binding.LocalLocator
				relative = executable.RelativePath
			case "system":
				locator, err := exec.LookPath(executable.Tool + ".exe")
				if err != nil || !filepath.IsAbs(locator) {
					result = append(result, invocationIssue(id+":executable", check.Missing, "SYSTEM_TOOL_MISSING"))
					continue
				}
				root = filepath.Dir(locator)
				relative = filepath.Base(locator)
			case "runtime":
				requirement, ok := req.Document.Requirements[executable.RequirementID]
				if !ok {
					result = append(result, invocationIssue(id+":executable", check.Unknown, "RUNTIME_REQUIREMENT_MISSING"))
					continue
				}
				program, _, err := toolArgs(requirement.Component)
				if err != nil || program == "" {
					result = append(result, invocationIssue(id+":executable", check.Unknown, "RUNTIME_TARGET_UNVERIFIED"))
					continue
				}
				locator := ""
				if binding, ok := req.Bindings[executable.RequirementID]; ok && binding.Kind == "runtime" {
					locator = binding.LocalLocator
				}
				if locator == "" {
					locator, err = exec.LookPath(program)
				}
				if err != nil || locator == "" || !filepath.IsAbs(locator) {
					result = append(result, invocationIssue(id+":executable", check.Missing, "RUNTIME_EXECUTABLE_MISSING"))
					continue
				}
				if info, err := os.Stat(locator); err == nil && info.IsDir() {
					root = locator
				} else {
					root = filepath.Dir(locator)
					// Explicit SDK-relative paths such as bin/go.exe identify their
					// base by the known absolute executable suffix, never by guessing cwd.
					suffix := "/" + executable.RelativePath
					if strings.HasSuffix(strings.ToLower(filepath.ToSlash(locator)), strings.ToLower(suffix)) {
						root = filepath.FromSlash(filepath.ToSlash(locator)[:len(filepath.ToSlash(locator))-len(suffix)])
					}
				}
				relative = executable.RelativePath
			}
			if !invocationPath(root, relative, false) {
				result = append(result, invocationIssue(id+":executable", check.Missing, "COMMAND_EXECUTABLE_MISSING"))
			}
		}
	}
	return result
}

func invocationIssue(id string, status check.Status, code string) check.Item {
	return check.Item{RequirementID: "command:" + id, Status: status, Reasons: []check.Reason{{Code: code, Subject: id, Message: "The declared executable or working directory must exist on this computer."}}}
}

func invocationPath(root, relative string, directory bool) bool {
	if !filepath.IsAbs(root) || strings.HasPrefix(root, `\\`) || project.ValidateRelativePath(relative) != nil {
		return false
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	anchor, err := os.OpenRoot(root)
	if err != nil {
		return false
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
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	info, err = anchor.Lstat(relative)
	if err != nil {
		return false
	}
	if directory {
		return info.IsDir()
	}
	return info.Mode().IsRegular()
}
