package environment

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/preparation"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

const projectEnvironmentMarker = ".lph-environment.json"

var preparationKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var preparationProjectID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// NativeProjectPreparer never restores packages into an original source directory.
// Each attempt receives a fresh permanent location: Python venv paths are not relocatable.
type NativeProjectPreparer struct {
	root        string
	workspaceID string
	runner      ports.PreparationRunner
	mu          sync.Mutex
}

// NewProjectPreparer requires an initialized workspace; it does not create one implicitly.
func NewProjectPreparer(root string, runner ports.PreparationRunner) (*NativeProjectPreparer, error) {
	anchor, err := filesystem.OpenLocalRoot(root)
	if err != nil {
		return nil, err
	}
	defer anchor.Close()
	data, err := readSmallRegularFile(filepath.Join(root, "workspace.json"), 1024*1024)
	if err != nil {
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	doc, err := contract.DecodeWorkspace(data)
	if err != nil {
		return nil, err
	}
	if info, err := anchor.Lstat("workspace.db"); err != nil || !info.Mode().IsRegular() {
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	return &NativeProjectPreparer{root: filepath.Clean(root), workspaceID: doc.WorkspaceID, runner: runner}, nil
}

// Plan inventories complete selected source roots and declares package installation side effects.
func (p *NativeProjectPreparer) Plan(
	ctx context.Context,
	req ports.ProjectPreparationRequest,
) (preparation.Plan, error) {
	plan := preparation.Plan{
		WorkspaceID:         p.workspaceID,
		Ref:                 req.Ref,
		Sources:             []preparation.Source{},
		Restores:            []preparation.Restore{},
		RuntimeFingerprints: map[string]string{},
		Warnings: []string{
			"SOURCE_COPY_EXCLUDES_LOCAL_DATA_AND_SECRETS",
			"DYNAMIC_SCRIPT_INPUTS_REQUIRE_DECLARATIONS",
		},
	}
	if !preparationProjectID.MatchString(string(req.Ref.ProjectID)) ||
		!preparationKey.MatchString(string(req.Ref.ProfileID)) ||
		req.Ref.ExpectedRevision < 1 {
		return plan, problem.New("PLAN_REFERENCE_REQUIRED", "")
	}
	directories := map[string]map[string]bool{}
	components := map[string]string{}
	for _, id := range req.UnitIDs {
		unit := req.Document.Units[id]
		for _, key := range unit.RequirementIDs {
			req := req.Document.Requirements[key]
			if !req.Required || req.Location == "docker" {
				continue
			}
			if existing := components[req.Component]; existing != "" && existing != key {
				return plan, problem.New("MULTIPLE_RUNTIME_BINDINGS_REQUIRE_MANUAL_PREPARATION", req.Component)
			}
			components[req.Component] = key
		}
		commands := []*project.Command{unit.Start, unit.Command}
		if unit.Stop != nil {
			commands = append(commands, unit.Stop.Command)
		}
		for _, command := range commands {
			if command == nil {
				continue
			}
			cwd := command.CWD
			if !preparationKey.MatchString(cwd.Binding) || project.ValidateRelativePath(cwd.RelativePath) != nil {
				return plan, problem.New("SOURCE_BINDING_INVALID", cwd.Binding)
			}
			if directories[cwd.Binding] == nil {
				directories[cwd.Binding] = map[string]bool{".": true}
			}
			directories[cwd.Binding][cwd.RelativePath] = true
		}
	}
	keys := []string{}
	for key := range directories {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		binding, ok := req.Bindings[key]
		if !ok || binding.Kind != "path" || binding.Key != key {
			return plan, problem.New("SOURCE_BINDING_REQUIRED", key)
		}
		// A workspace below the source would recursively copy its own preparations.
		if pathInside(binding.LocalLocator, p.root) {
			return plan, problem.New("SOURCE_CONTAINS_WORKSPACE", key)
		}
		files, err := scanSource(ctx, binding)
		if err != nil {
			return plan, err
		}
		for id, file := range req.Document.Files {
			if file.Path == nil || file.Path.Binding != key || !file.Required || file.Availability == "generated" {
				continue
			}
			if excludedSource(file.Path.RelativePath) {
				return plan, problem.New("SOURCE_REQUIRED_FILE_EXCLUDED", id)
			}
			if !slices.ContainsFunc(files, func(f preparation.File) bool { return f.Path == file.Path.RelativePath }) {
				return plan, problem.New("SOURCE_REQUIRED_FILE_MISSING", id)
			}
			base := path.Base(file.Path.RelativePath)
			if base == "package.json" || base == "package-lock.json" || base == "requirements.txt" ||
				base == "go.mod" ||
				base == "pyproject.toml" {
				directories[key][path.Dir(file.Path.RelativePath)] = true
			}
		}
		dirs := []string{}
		for dir := range directories[key] {
			if !invocationPath(binding.LocalLocator, dir, true) {
				return plan, problem.New("COMMAND_DIRECTORY_MISSING", key)
			}
			dirs = append(dirs, dir)
		}
		sort.Strings(dirs)
		plan.Sources = append(plan.Sources, preparation.Source{Binding: binding, Files: files, Directories: dirs})
		for _, dir := range dirs {
			if err := planRestores(&plan, binding, dir, files, components); err != nil {
				return plan, err
			}
		}
	}
	if len(plan.Sources) == 0 {
		return plan, problem.New("SOURCE_BINDING_REQUIRED", "")
	}
	for key, runtime := range req.RuntimePlans {
		plan.RuntimeFingerprints[key] = runtime.Fingerprint
		if binding, ok := req.Bindings[key]; ok {
			plan.PreviousRuntimeBindings = append(plan.PreviousRuntimeBindings, binding)
		}
	}
	sort.Slice(
		plan.PreviousRuntimeBindings,
		func(i, j int) bool { return plan.PreviousRuntimeBindings[i].Key < plan.PreviousRuntimeBindings[j].Key },
	)
	for _, restore := range plan.Restores {
		if plan.RuntimeFingerprints[restore.RequirementID] == "" {
			return plan, problem.New("RUNTIME_PREPARATION_REQUIRED", restore.RequirementID)
		}
	}
	venvs := map[string]bool{}
	for _, restore := range plan.Restores {
		if restore.Effect != "create-venv" {
			continue
		}
		if venvs[restore.RequirementID] {
			return plan, problem.New("MULTIPLE_PYTHON_ENVIRONMENTS_REQUIRE_MANUAL_PREPARATION", restore.RequirementID)
		}
		venvs[restore.RequirementID] = true
	}
	plan.Fingerprint = sourcePlanFingerprint(plan)
	return plan, nil
}

func planRestores(
	plan *preparation.Plan,
	binding project.Binding,
	dir string,
	files []preparation.File,
	components map[string]string,
) error {
	has := func(name string) bool {
		return slices.ContainsFunc(files, func(f preparation.File) bool { return f.Path == path.Join(dir, name) })
	}
	cwd := project.Path{Binding: binding.Key, RelativePath: dir}
	add := func(key, tool, effect string, args ...string) {
		plan.Restores = append(
			plan.Restores,
			preparation.Restore{RequirementID: key, Tool: tool, Directory: cwd, Effect: effect, Args: args},
		)
	}
	if key := components["node"]; key != "" && has("package.json") {
		if !has("package-lock.json") {
			return problem.New("NODE_LOCKFILE_REQUIRED", binding.Key+":"+dir)
		}
		data, err := readSmallRegularFile(
			filepath.Join(binding.LocalLocator, filepath.FromSlash(path.Join(dir, "package-lock.json"))),
			16*1024*1024,
		)
		if err != nil {
			return err
		}
		var lock struct {
			LockfileVersion int `json:"lockfileVersion"`
		}
		if json.Unmarshal(data, &lock) != nil || lock.LockfileVersion < 2 || lock.LockfileVersion > 3 {
			return problem.New("NODE_LOCKFILE_UNSUPPORTED", dir)
		}
		add(key, "npm", "package-install-scripts", "ci", "--no-audit", "--no-fund")
	}
	if key := components["python"]; key != "" {
		if has("pyproject.toml") && !has("requirements.txt") {
			return problem.New("PYTHON_LOCK_RESTORE_REQUIRES_MANUAL_STEPS", dir)
		}
		if has("requirements.txt") || dir == "." {
			add(key, "runtime", "create-venv", "-I", "-B", "-m", "venv", ".venv")
			if has("requirements.txt") {
				// Requirements can invoke build backends. This is an explicit install
				// effect and is never part of a read-only environment check.
				add(
					key,
					"venv-python",
					"package-install-scripts",
					"-I",
					"-B",
					"-m",
					"pip",
					"--isolated",
					"install",
					"--disable-pip-version-check",
					"--no-input",
					"-r",
					"requirements.txt",
				)
				plan.Warnings = append(plan.Warnings, "PYTHON_REQUIREMENTS_MAY_NOT_PIN_TRANSITIVE_DEPENDENCIES")
			}
			add(key, "venv-python", "verify-dependencies", "-I", "-B", "-m", "pip", "--isolated", "check")
		}
	}
	if key := components["go"]; key != "" && has("go.mod") {
		add(key, "runtime", "download-modules", "mod", "download")
		add(key, "runtime", "verify-dependencies", "mod", "verify")
	}
	return nil
}

// Prepare leaves no active selection on failure; only a complete receipt can be activated.
func (p *NativeProjectPreparer) Prepare(
	ctx context.Context,
	plan preparation.Plan,
	runtimes map[string]ports.RuntimeLocation,
) (preparation.Receipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var receipt preparation.Receipt
	if plan.WorkspaceID != p.workspaceID || plan.Fingerprint != sourcePlanFingerprint(plan) ||
		!preparationProjectID.MatchString(string(plan.Ref.ProjectID)) ||
		!preparationKey.MatchString(string(plan.Ref.ProfileID)) {
		return receipt, problem.New("SOURCE_PLAN_STALE", "")
	}
	for _, source := range plan.Sources {
		if !preparationKey.MatchString(source.Binding.Key) || source.Binding.Kind != "path" {
			return receipt, problem.New("SOURCE_PLAN_STALE", "")
		}
		files, err := scanSource(ctx, source.Binding)
		if err != nil {
			return receipt, err
		}
		if !slices.Equal(files, source.Files) {
			return receipt, problem.New("SOURCE_PLAN_STALE", source.Binding.Key)
		}
	}
	for key, fingerprint := range plan.RuntimeFingerprints {
		location, ok := runtimes[key]
		if !ok || location.Fingerprint != fingerprint || !pathInside(filepath.Join(p.root, "runtimes"), location.Root) {
			return receipt, problem.New("RUNTIME_PLAN_STALE", key)
		}
	}
	anchor, err := filesystem.OpenLocalRoot(p.root)
	if err != nil {
		return receipt, err
	}
	defer anchor.Close()
	nonce, err := identity.NewUUID()
	if err != nil {
		return receipt, err
	}
	relative := path.Join("envs", string(plan.Ref.ProjectID), string(plan.Ref.ProfileID), nonce)
	if err := anchor.MkdirAll(path.Dir(relative), 0700); err != nil {
		return receipt, err
	}
	if err := anchor.Mkdir(relative, 0700); err != nil {
		return receipt, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = anchor.RemoveAll(relative)
		}
	}()
	receipt = preparation.Receipt{
		WorkspaceID:      p.workspaceID,
		Ref:              plan.Ref,
		Root:             filepath.Join(p.root, filepath.FromSlash(relative)),
		State:            "preparing",
		PlanFingerprint:  plan.Fingerprint,
		Bindings:         []project.Binding{},
		OriginalBindings: []project.Binding{},
	}
	for _, source := range plan.Sources {
		destination := path.Join(relative, "sources", source.Binding.Key)
		if err := copySource(ctx, anchor, destination, source); err != nil {
			return preparation.Receipt{}, err
		}
		for _, dir := range source.Directories {
			if project.ValidateRelativePath(dir) != nil {
				return preparation.Receipt{}, problem.New("SOURCE_PLAN_STALE", "")
			}
			if err := anchor.MkdirAll(path.Join(destination, dir), 0700); err != nil {
				return preparation.Receipt{}, err
			}
		}
		receipt.OriginalBindings = append(receipt.OriginalBindings, source.Binding)
		receipt.Bindings = append(
			receipt.Bindings,
			project.Binding{
				Key:          source.Binding.Key,
				Kind:         "path",
				LocalLocator: filepath.Join(p.root, filepath.FromSlash(destination)),
			},
		)
	}
	receipt.OriginalBindings = append(receipt.OriginalBindings, plan.PreviousRuntimeBindings...)
	keys := []string{}
	for key := range plan.RuntimeFingerprints {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		receipt.Bindings = append(
			receipt.Bindings,
			project.Binding{Key: key, Kind: "runtime", LocalLocator: runtimes[key].Executable},
		)
	}
	if err := writePreparationReceipt(anchor, relative, receipt); err != nil {
		return preparation.Receipt{}, err
	}
	for _, restore := range plan.Restores {
		if err := ctx.Err(); err != nil {
			return preparation.Receipt{}, err
		}
		if p.runner == nil {
			return preparation.Receipt{}, problem.New("DEPENDENCY_RUNNER_UNAVAILABLE", "")
		}
		command, err := preparationCommand(receipt, restore, runtimes)
		if err != nil {
			return preparation.Receipt{}, err
		}
		result, err := p.runner.Run(ctx, command)
		if err != nil {
			return preparation.Receipt{}, err
		}
		if result.ExitCode != 0 {
			return preparation.Receipt{}, problem.New("DEPENDENCY_RESTORE_FAILED", restore.Directory.Binding)
		}
	}
	if err := ctx.Err(); err != nil {
		return preparation.Receipt{}, err
	}
	for _, restore := range plan.Restores {
		if restore.Effect != "create-venv" {
			continue
		}
		for i := range receipt.Bindings {
			if receipt.Bindings[i].Key != restore.RequirementID {
				continue
			}
			for _, binding := range receipt.Bindings {
				if binding.Key == restore.Directory.Binding {
					receipt.Bindings[i].LocalLocator = filepath.Join(
						binding.LocalLocator,
						filepath.FromSlash(restore.Directory.RelativePath),
						".venv",
						"Scripts",
						"python.exe",
					)
				}
			}
		}
	}
	receipt.State = "ready"
	if err := writePreparationReceipt(anchor, relative, receipt); err != nil {
		return preparation.Receipt{}, err
	}
	complete = true
	return receipt, nil
}

func preparationCommand(
	receipt preparation.Receipt,
	restore preparation.Restore,
	runtimes map[string]ports.RuntimeLocation,
) (ports.PreparationCommand, error) {
	var root string
	for _, binding := range receipt.Bindings {
		if binding.Key == restore.Directory.Binding {
			root = binding.LocalLocator
		}
	}
	if root == "" || project.ValidateRelativePath(restore.Directory.RelativePath) != nil {
		return ports.PreparationCommand{}, problem.New("SOURCE_PLAN_STALE", "")
	}
	runtime, ok := runtimes[restore.RequirementID]
	if !ok {
		return ports.PreparationCommand{}, problem.New("RUNTIME_PREPARATION_REQUIRED", restore.RequirementID)
	}
	cwd := filepath.Join(root, filepath.FromSlash(restore.Directory.RelativePath))
	program, args := runtime.Executable, slices.Clone(restore.Args)
	switch restore.Tool {
	case "npm":
		args = append([]string{filepath.Join(runtime.Root, "node_modules", "npm", "bin", "npm-cli.js")}, args...)
	case "venv-python":
		program = filepath.Join(cwd, ".venv", "Scripts", "python.exe")
	case "runtime":
	default:
		return ports.PreparationCommand{}, problem.New("PREPARATION_TOOL_UNSUPPORTED", "")
	}
	cache := filepath.Join(receipt.Root, "cache")
	env := map[string]string{
		"NPM_CONFIG_CACHE": filepath.Join(
			cache,
			"npm",
		), "NPM_CONFIG_USERCONFIG": filepath.Join(cache, "npmrc"), "NPM_CONFIG_GLOBALCONFIG": filepath.Join(cache, "global-npmrc"),
		"PIP_CACHE_DIR": filepath.Join(cache, "pip"), "PIP_CONFIG_FILE": "NUL", "PYTHONNOUSERSITE": "1",
		"PYTHONDONTWRITEBYTECODE": "1", "PYTHONPYCACHEPREFIX": filepath.Join(cache, "python-bytecode"),
		"GOCACHE": filepath.Join(
			cache,
			"go-build",
		), "GOMODCACHE": filepath.Join(cache, "go-mod"), "GOPATH": filepath.Join(cache, "go"), "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": "-mod=readonly", "GOENV": "off",
	}
	// Child-only PATH includes the pinned SDKs; no global process environment is changed.
	keys := []string{}
	for key := range runtimes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	paths := []string{filepath.Dir(program)}
	for _, key := range keys {
		paths = append(paths, filepath.Dir(runtimes[key].Executable))
	}
	env["PATH"] = strings.Join(paths, string(os.PathListSeparator))
	return ports.PreparationCommand{
		Executable:  program,
		Args:        args,
		CWD:         cwd,
		OwnedRoot:   receipt.Root,
		Environment: env,
		Timeout:     20 * time.Minute,
	}, nil
}

func writePreparationReceipt(anchor *os.Root, relative string, receipt preparation.Receipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	name := path.Join(relative, projectEnvironmentMarker)
	file, err := anchor.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	syncErr, closeErr := file.Sync(), file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func pathInside(root, candidate string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(candidate) {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}
