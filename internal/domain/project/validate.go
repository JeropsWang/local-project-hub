package project

import (
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
)

var localID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var placeholder = regexp.MustCompile(`\$\{([^}]+)\}`)

// Validate checks cross-references and portable lexical constraints after Schema
// validation. It does not touch the filesystem or grant permission to execute.
func Validate(doc Document) error {
	if doc.SchemaVersion != "1.0" || !uuid.MatchString(doc.ProjectID) || strings.TrimSpace(doc.Name) == "" {
		return problem.New("PROJECT_INVALID", "")
	}
	if len(doc.Units) == 0 || len(doc.Units) > 128 || len(doc.Profiles) == 0 || len(doc.Profiles) > 32 {
		return problem.New("PROJECT_LIMIT", "")
	}
	for id, req := range doc.Requirements {
		if !localID.MatchString(id) || req.Range == "" || len([]rune(req.Range)) > 512 {
			return problem.New("REQUIREMENT_INVALID", id)
		}
	}
	includedPaths := map[string]bool{}
	for id, file := range doc.Files {
		if !localID.MatchString(id) {
			return problem.New("FILE_INVALID", id)
		}
		switch file.Availability {
		case "included":
			canonical := strings.ToUpper(file.BundlePath)
			if includedPaths[canonical] {
				return problem.New("FILE_PATH_CONFLICT", id)
			}
			includedPaths[canonical] = true
			if err := ValidateRelativePath(file.BundlePath); err != nil {
				return err
			}
			if !strings.HasPrefix(file.BundlePath, "files/") {
				return problem.New("FILE_PATH_INVALID", id)
			}
		case "generated", "external":
			if err := validatePath(file.Path); err != nil {
				return err
			}
			if file.Availability == "generated" {
				generator, ok := doc.Units[file.GeneratorUnitID]
				if !ok || generator.Kind != "task" {
					return problem.New("GENERATOR_MISSING", id)
				}
			}
		default:
			return problem.New("FILE_INVALID", id)
		}
	}
	if doc.Appearance != nil && doc.Appearance.CoverFileID != "" {
		file, ok := doc.Files[doc.Appearance.CoverFileID]
		if !ok || file.Role != "cover" {
			return problem.New("COVER_INVALID", "")
		}
	}
	for id, unit := range doc.Units {
		if !localID.MatchString(id) || !uuid.MatchString(unit.ResourceID) {
			return problem.New("UNIT_INVALID", id)
		}
		for _, dependency := range unit.DependsOn {
			if _, ok := doc.Units[dependency]; !ok {
				return problem.New("UNIT_REFERENCE_MISSING", id)
			}
		}
		for _, requirement := range unit.RequirementIDs {
			if _, ok := doc.Requirements[requirement]; !ok {
				return problem.New("REQUIREMENT_MISSING", id)
			}
		}
		if err := validateUnit(doc, unit); err != nil {
			return err
		}
	}
	// Reject cycles even in units not selected by the current profile.
	allIDs := make([]string, 0, len(doc.Units))
	for id := range doc.Units {
		allIDs = append(allIDs, id)
	}
	sort.Strings(allIDs)
	if _, err := ordered(doc, allIDs, nil); err != nil {
		return err
	}
	for id, profile := range doc.Profiles {
		if !localID.MatchString(id) || len(profile.UnitIDs) == 0 {
			return problem.New("PROFILE_INVALID", id)
		}
		if _, err := ordered(doc, profile.UnitIDs, append([]string{}, profile.FeatureIDs...)); err != nil {
			return err
		}
	}
	return nil
}

func validatePath(value *Path) error {
	if value == nil || !localID.MatchString(value.Binding) {
		return problem.New("PATH_BINDING_INVALID", "")
	}
	return ValidateRelativePath(value.RelativePath)
}

func validateUnit(doc Document, unit Unit) error {
	switch unit.Kind {
	case "process":
		if unit.Start == nil || unit.Readiness == nil || unit.Stop == nil {
			return problem.New("PROCESS_POLICY_MISSING", "")
		}
	case "compose":
		if len(unit.ComposeFileIDs) == 0 || len(unit.Services) == 0 || !localID.MatchString(unit.EngineBinding) ||
			unit.Readiness == nil || unit.Stop == nil {
			return problem.New("COMPOSE_POLICY_MISSING", "")
		}
		for _, id := range unit.ComposeFileIDs {
			file, ok := doc.Files[id]
			if !ok || file.Role != "compose" {
				return problem.New("COMPOSE_FILE_MISSING", id)
			}
		}
	case "task":
		if unit.Command == nil {
			return problem.New("TASK_COMMAND_MISSING", "")
		}
	case "external":
		if unit.Control != "reference" || !localID.MatchString(unit.Binding) || unit.Readiness == nil {
			return problem.New("EXTERNAL_POLICY_INVALID", "")
		}
	default:
		return problem.New("UNIT_KIND_INVALID", "")
	}
	if unit.Control != "managed" && unit.Control != "reference" {
		return problem.New("CONTROL_INVALID", "")
	}
	commands := []*Command{unit.Start, unit.Command}
	if unit.Stop != nil {
		if unit.Stop.Kind == "command" && unit.Stop.Command == nil {
			return problem.New("STOP_COMMAND_MISSING", "")
		}
		commands = append(commands, unit.Stop.Command)
	}
	for _, command := range commands {
		if command != nil {
			if command.Executable.Kind == "runtime" && !slices.Contains(unit.RequirementIDs, command.Executable.RequirementID) {
				return problem.New("UNIT_RUNTIME_NOT_SELECTED", command.Executable.RequirementID)
			}
			if err := validateCommand(doc, command); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateCommand(doc Document, command *Command) error {
	if err := validatePath(&command.CWD); err != nil {
		return err
	}
	switch executable := command.Executable; executable.Kind {
	case "runtime":
		if _, ok := doc.Requirements[executable.RequirementID]; !ok {
			return problem.New("RUNTIME_MISSING", "")
		}
		if err := ValidateRelativePath(executable.RelativePath); err != nil {
			return err
		}
	case "binding":
		if !localID.MatchString(executable.Binding) {
			return problem.New("PATH_BINDING_INVALID", "")
		}
		if err := ValidateRelativePath(executable.RelativePath); err != nil {
			return err
		}
	case "system":
		if !slices.Contains([]string{"powershell", "pwsh", "cmd", "docker", "git"}, executable.Tool) {
			return problem.New("SYSTEM_TOOL_UNSUPPORTED", "")
		}
	default:
		return problem.New("EXECUTABLE_INVALID", "")
	}
	seen := map[string]bool{}
	for name, binding := range command.EnvBindings {
		canonical := strings.ToUpper(name)
		if !envName.MatchString(name) || !localID.MatchString(binding) || seen[canonical] {
			return problem.New("ENV_BINDING_INVALID", "")
		}
		if canonical == "LPH_RUN_ID" || canonical == "LPH_CONTROL_PIPE" || canonical == "LPH_CONTROL_TOKEN" {
			return problem.New("ENV_RESERVED", "")
		}
		seen[canonical] = true
	}
	for _, arg := range command.Args {
		matches := placeholder.FindAllStringSubmatch(arg, -1)
		for _, match := range matches {
			key, ok := strings.CutPrefix(match[1], "file:")
			if !ok {
				return problem.New("COMMAND_PLACEHOLDER_INVALID", "")
			}
			if _, exists := doc.Files[key]; !exists {
				return problem.New("FILE_REFERENCE_MISSING", key)
			}
		}
		if strings.Contains(placeholder.ReplaceAllString(arg, ""), "$"+"{") {
			return problem.New("COMMAND_PLACEHOLDER_INVALID", "")
		}
	}
	return nil
}

// Order returns deterministic dependency-first order for a profile. Missing and
// disabled dependencies are errors; optional features are never silently enabled.
func Order(doc Document, profileID string) ([]string, error) {
	if err := Validate(doc); err != nil {
		return nil, err
	}
	profile, ok := doc.Profiles[profileID]
	if !ok {
		return nil, problem.New("PROFILE_MISSING", profileID)
	}
	return ordered(doc, profile.UnitIDs, append([]string{}, profile.FeatureIDs...))
}

func ordered(doc Document, roots []string, features []string) ([]string, error) {
	state := map[string]uint8{}
	order := []string{}
	var visit func(string) error
	visit = func(id string) error {
		unit, exists := doc.Units[id]
		if !exists {
			return problem.New("UNIT_REFERENCE_MISSING", id)
		}
		if features != nil && unit.FeatureID != "" && !slices.Contains(features, unit.FeatureID) {
			return problem.New("FEATURE_DISABLED_DEPENDENCY", id)
		}
		switch state[id] {
		case 1:
			return problem.New("DEPENDENCY_CYCLE", "")
		case 2:
			return nil
		}
		state[id] = 1
		dependencies := append([]string{}, unit.DependsOn...)
		sort.Strings(dependencies)
		for _, dependency := range dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[id] = 2
		order = append(order, id)
		return nil
	}
	for _, id := range roots {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return order, nil
}
