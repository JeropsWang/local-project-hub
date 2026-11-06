package bundle

import (
	"path"
	"slices"
	"strings"

	"golang.org/x/text/cases"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

type FileReader func(string) ([]byte, error)

type Closure struct {
	Files               map[string][]byte
	ExternalBindingKeys []string
	Warnings            []string
}

// Collect follows the portable declarations only. A binding is exported as its
// key, while script dependencies still require an explicit declaration.
func Collect(doc project.Document, read FileReader) (Closure, error) {
	result := Closure{Files: map[string][]byte{}, ExternalBindingKeys: []string{}}
	keys := map[string]bool{}
	add := func(key string) {
		if key != "" {
			keys[key] = true
		}
	}
	aliases := map[string]bool{}
	total := 0
	for _, file := range doc.Files {
		if file.Path != nil {
			add(file.Path.Binding)
		}
		if file.Availability != "included" {
			continue
		}
		name := file.BundlePath
		if err := ValidateFilePath(name); err != nil {
			return Closure{}, err
		}
		key := PathKey(name)
		if aliases[key] {
			return Closure{}, problem.New("BUNDLE_PATH_CONFLICT", name)
		}
		aliases[key] = true
		data, err := read(name)
		if err != nil {
			return Closure{}, err
		}
		total += len(data)
		if len(data) > MaxFile || total > MaxExpanded || len(result.Files) >= MaxFiles-1 {
			return Closure{}, problem.New("BUNDLE_LIMIT", name)
		}
		if file.ExpectedSHA256 != "" && Digest(data) != file.ExpectedSHA256 {
			return Closure{}, problem.New("REVISION_FILE_CHANGED", name)
		}
		result.Files[name] = data
		if file.Role == "script" && !slices.Contains(result.Warnings, "SCRIPT_DYNAMIC_REFERENCES_UNVERIFIED") {
			result.Warnings = append(result.Warnings, "SCRIPT_DYNAMIC_REFERENCES_UNVERIFIED")
		}
	}
	for key, requirement := range doc.Requirements {
		if requirement.Location == "windows" {
			add(key)
		}
	}
	for _, unit := range doc.Units {
		add(unit.EngineBinding)
		add(unit.Binding)
		commands := []*project.Command{unit.Start, unit.Command}
		if unit.Stop != nil {
			commands = append(commands, unit.Stop.Command)
		}
		for _, command := range commands {
			if command == nil {
				continue
			}
			add(command.CWD.Binding)
			add(command.Executable.Binding)
			add(command.Executable.RequirementID)
			for _, key := range command.EnvBindings {
				add(key)
			}
		}
	}
	for key := range keys {
		result.ExternalBindingKeys = append(result.ExternalBindingKeys, key)
	}
	slices.Sort(result.ExternalBindingKeys)
	return result, nil
}

// PathKey folds Windows aliases, including Unicode case variants.
func PathKey(name string) string { return cases.Fold().String(name) }

// ValidateFilePath admits configuration payload paths and rejects known local
// state and credential files regardless of their declared role.
func ValidateFilePath(name string) error {
	if project.ValidateRelativePath(name) != nil || !strings.HasPrefix(name, "files/") {
		return problem.New("BUNDLE_PATH_INVALID", name)
	}
	for _, part := range strings.Split(PathKey(name), "/") {
		if part == ".git" || part == ".venv" || part == "node_modules" || part == "secrets" ||
			(strings.HasPrefix(part, ".env") && part != ".env.example" && part != ".env.template") {
			return problem.New("BUNDLE_LOCAL_FILE_EXCLUDED", name)
		}
	}
	switch path.Ext(PathKey(name)) {
	case ".dpapi", ".db", ".sqlite", ".sqlite3", ".exe", ".dll", ".pfx", ".p12", ".key":
		return problem.New("BUNDLE_LOCAL_FILE_EXCLUDED", name)
	}
	return nil
}
