package environment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	versions "github.com/JeropsWang/local-project-hub/internal/adapters/version"
	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// inspectConstraints reads only conventional/declaratively referenced manifests.
// It never invokes npm, Maven, Gradle, Go downloads or project lifecycle hooks.
func inspectConstraints(ctx context.Context, req ports.InspectionRequest, result *ports.SourceInspection, fingerprint io.Writer, total *int64) error {
	relevant := map[string]bool{}
	for _, requirement := range req.Document.Requirements {
		relevant[requirement.Component] = true
	}
	if req.UnitIDs != nil {
		relevant = map[string]bool{}
		for _, id := range req.UnitIDs {
			for _, key := range req.Document.Units[id].RequirementIDs {
				relevant[req.Document.Requirements[key].Component] = true
			}
		}
	}
	bindingKeys := make([]string, 0, len(req.Bindings))
	for key := range req.Bindings {
		bindingKeys = append(bindingKeys, key)
	}
	sort.Strings(bindingKeys)
	for _, bindingKey := range bindingKeys {
		binding := req.Bindings[bindingKey]
		if binding.Kind != "path" {
			continue
		}
		source := bindingKey == "source"
		names := map[string]bool{"package.json": true, "package-lock.json": true, "go.mod": true, "go.work": true, "pom.xml": true}
		for _, name := range []string{"pyproject.toml", "uv.lock", "poetry.lock", "PKG-INFO", "requirements.txt"} {
			names[name] = true
		}
		for _, file := range req.Document.Files {
			if file.Path != nil && file.Path.Binding == bindingKey {
				if file.Role == "source" {
					source = true
				}
				switch filepath.Base(file.Path.RelativePath) {
				case "package.json", "package-lock.json", "go.mod", "go.work", "pom.xml":
					names[file.Path.RelativePath] = true
				}
				if file.Required && isPythonManifest(filepath.Base(file.Path.RelativePath)) {
					names[file.Path.RelativePath] = true
				}
			}
		}
		if !source || !filepath.IsAbs(binding.LocalLocator) || strings.HasPrefix(binding.LocalLocator, `\\`) {
			continue
		}
		anchor, err := os.OpenRoot(binding.LocalLocator)
		if err != nil {
			continue
		}
		manifestNames := make([]string, 0, len(names))
		for name := range names {
			manifestNames = append(manifestNames, name)
		}
		sort.Strings(manifestNames)
		for _, name := range manifestNames {
			if err := ctx.Err(); err != nil {
				anchor.Close()
				return err
			}
			base := filepath.Base(name)
			if (base == "package.json" || base == "package-lock.json") && !relevant["node"] && !relevant["npm"] {
				continue
			}
			if (base == "go.mod" || base == "go.work") && !relevant["go"] {
				continue
			}
			if base == "pom.xml" && !relevant["java"] {
				continue
			}
			if isPythonManifest(base) && !relevant["python"] {
				continue
			}
			data, err := readManifest(anchor, name, total)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				appendMetadataIssue(result, name, "METADATA_READ_UNVERIFIED")
				continue
			}
			digest := sha256.Sum256(data)
			_, _ = io.WriteString(fingerprint, bindingKey+":"+name+":"+hex.EncodeToString(digest[:])+";")
			constraints, err := parseManifest(base, data)
			if err != nil {
				code := "METADATA_FORMAT_UNSUPPORTED"
				if isPythonManifest(base) {
					code = "PYTHON_METADATA_UNVERIFIED"
				}
				appendMetadataIssue(result, name, code)
				continue
			}
			for _, constraint := range constraints {
				constraint.Source = bindingKey + "/" + name
				result.Constraints = append(result.Constraints, constraint)
				if len(result.Constraints) > 4096 {
					anchor.Close()
					appendMetadataIssue(result, name, "METADATA_CONSTRAINT_LIMIT")
					return nil
				}
			}
		}
		anchor.Close()
	}
	// Map traversal order must not alter visible constraint order or fingerprints.
	sort.Slice(result.Constraints, func(i, j int) bool {
		a, b := result.Constraints[i], result.Constraints[j]
		return a.Source+a.Component+a.Range < b.Source+b.Component+b.Range
	})
	return nil
}

func appendMetadataIssue(result *ports.SourceInspection, name, code string) {
	result.Items = append(result.Items, check.Item{RequirementID: "metadata:" + name, Status: check.Unknown, Reasons: []check.Reason{{Code: code, Subject: name}}})
}

func readManifest(root *os.Root, name string, total *int64) ([]byte, error) {
	current := ""
	for _, part := range strings.Split(name, "/") {
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		info, err := root.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, os.ErrPermission
		}
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16*1024*1024 || *total+info.Size() > 128*1024*1024 {
		return nil, os.ErrPermission
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16*1024*1024 {
		return nil, os.ErrPermission
	}
	*total += int64(len(data))
	if *total > 128*1024*1024 {
		return nil, os.ErrPermission
	}
	return data, nil
}

type engineDeclaration struct {
	Engines map[string]string `json:"engines"`
	OS      []string          `json:"os"`
	CPU     []string          `json:"cpu"`
}

func parseManifest(name string, data []byte) ([]ports.MetadataConstraint, error) {
	if isPythonManifest(name) {
		return parsePythonManifest(name, data)
	}
	result := []ports.MetadataConstraint{}
	addEngines := func(engine engineDeclaration) {
		if !appliesTo(engine.OS, "win32") || !appliesTo(engine.CPU, "x64") {
			return
		}
		for _, component := range []string{"node", "npm"} {
			if text := engine.Engines[component]; text != "" {
				result = append(result, ports.MetadataConstraint{Component: component, Semantics: "npm", Range: text})
			}
		}
	}
	switch name {
	case "package.json":
		var declaration engineDeclaration
		if err := json.Unmarshal(data, &declaration); err != nil {
			return nil, err
		}
		addEngines(declaration)
	case "package-lock.json":
		var lock struct {
			LockfileVersion int                          `json:"lockfileVersion"`
			Packages        map[string]engineDeclaration `json:"packages"`
		}
		if err := json.Unmarshal(data, &lock); err != nil {
			return nil, err
		}
		if lock.LockfileVersion < 2 || lock.LockfileVersion > 3 || lock.Packages == nil {
			return nil, os.ErrInvalid
		}
		keys := []string{}
		for key := range lock.Packages {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		seen := map[string]bool{}
		for _, key := range keys {
			before := len(result)
			addEngines(lock.Packages[key])
			for i := before; i < len(result); i++ {
				id := result[i].Component + "\x00" + result[i].Range
				if seen[id] {
					result = append(result[:i], result[i+1:]...)
					i--
				} else {
					seen[id] = true
				}
			}
		}
	case "go.mod", "go.work":
		declaration, err := versions.ParseGoModule(name, data)
		if err != nil {
			return nil, err
		}
		result = append(result, ports.MetadataConstraint{Component: "go", Semantics: "goversion", Range: ">=" + declaration.Minimum})
	case "pom.xml":
		declaration, err := versions.ParseMavenRequirements(data)
		if err != nil {
			return nil, err
		}
		result = append(result, ports.MetadataConstraint{Component: "java", Semantics: "java", Range: ">=" + strconv.Itoa(declaration.BuildJDKMinimum)})
	}
	return result, nil
}

func appliesTo(values []string, target string) bool {
	if len(values) == 0 {
		return true
	}
	hasPositive := false
	matches := false
	for _, value := range values {
		if value == "!"+target {
			return false
		}
		if !strings.HasPrefix(value, "!") {
			hasPositive = true
			if value == target || value == "any" {
				matches = true
			}
		}
	}
	return !hasPositive || matches
}
