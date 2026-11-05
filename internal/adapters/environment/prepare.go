package environment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	versions "github.com/JeropsWang/local-project-hub/internal/adapters/version"
	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

const runtimeMarker = ".lph-runtime.json"

// NativeRuntimeProvider is scoped to one explicitly initialized workspace.
type NativeRuntimeProvider struct {
	root        string
	workspaceID string
	artifacts   map[string]RuntimeArtifact
	client      *http.Client
	mu          sync.Mutex
}

// NewRuntimeProvider accepts a backend-maintained catalog and never initializes a workspace implicitly.
func NewRuntimeProvider(root string, catalog RuntimeCatalog, client *http.Client) (*NativeRuntimeProvider, error) {
	if !filepath.IsAbs(root) {
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	if err := validateCatalog(catalog); err != nil {
		return nil, err
	}
	data, err := readSmallRegularFile(filepath.Join(root, "workspace.json"), 1024*1024)
	if err != nil {
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	doc, err := contract.DecodeWorkspace(data)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(filepath.Join(root, "workspace.db")); err != nil || !info.Mode().IsRegular() {
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	clone := *client
	redirect := client.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 8 {
			return problem.New("RUNTIME_REDIRECT_REJECTED", "")
		}
		if redirect != nil {
			return redirect(req, via)
		}
		return nil
	}
	provider := &NativeRuntimeProvider{root: filepath.Clean(root), workspaceID: doc.WorkspaceID, artifacts: map[string]RuntimeArtifact{}, client: &clone}
	for _, artifact := range catalog.Artifacts {
		provider.artifacts[artifact.ID] = artifact
	}
	return provider, nil
}

// Plan chooses a catalog match and reports unsupported runtimes rather than invoking a generic installer.
func (p *NativeRuntimeProvider) Plan(ctx context.Context, req ports.RuntimeRequest) (ports.RuntimeInstallPlan, error) {
	if err := ctx.Err(); err != nil {
		return ports.RuntimeInstallPlan{}, err
	}
	component := req.Requirement.Component
	if component != "node" && component != "go" && component != "python" && component != "npm" {
		return ports.RuntimeInstallPlan{}, problem.New("AUTO_PREPARE_UNSUPPORTED", component)
	}
	matches := []RuntimeArtifact{}
	for _, artifact := range p.artifacts {
		actualComponent := artifact.Component
		version := artifact.Version
		if component == "npm" && artifact.Component == "node" {
			actualComponent = "npm"
			version = artifact.BundledNpm
		}
		if component != actualComponent {
			continue
		}
		item, _ := versions.Evaluate(req.Requirement, ports.Observation{Version: version, Architecture: artifact.Architecture, ABI: artifact.ABI, Evidence: check.Evidence{Kind: "metadata", Source: artifact.Source}})
		if item.Status == check.Satisfied {
			compatible := true
			for _, constraint := range req.Constraints {
				if constraint.Component != component {
					continue
				}
				extra := req.Requirement
				extra.Semantics = constraint.Semantics
				extra.Range = constraint.Range
				verdict, _ := versions.Evaluate(extra, ports.Observation{Version: version, Architecture: artifact.Architecture, ABI: artifact.ABI})
				if verdict.Status != check.Satisfied {
					compatible = false
					break
				}
			}
			if !compatible {
				continue
			}
			matches = append(matches, artifact)
		}
	}
	if len(matches) == 0 {
		return ports.RuntimeInstallPlan{}, problem.New("RUNTIME_VERSION_UNSUPPORTED", component)
	}
	sort.Slice(matches, func(i, j int) bool {
		// Preserve an explicit preferred minor/patch before choosing a fallback.
		pref := req.Requirement.PreferredVersion
		a := preferredRuntime(matches[i].Version, pref)
		b := preferredRuntime(matches[j].Version, pref)
		if a != b {
			return a
		}
		first, _ := semver.StrictNewVersion(matches[i].Version)
		second, _ := semver.StrictNewVersion(matches[j].Version)
		return first.GreaterThan(second)
	})
	plan := p.installPlan(matches[0])
	if location, err := p.installed(ctx, matches[0]); err == nil && location.Root != "" {
		plan.AlreadyInstalled = true
	}
	return plan, nil
}

func preferredRuntime(version, preferred string) bool {
	if preferred == "" {
		return false
	}
	return version == preferred || len(preferred) < len(version) && version[:len(preferred)] == preferred && version[len(preferred)] == '.'
}

func (p *NativeRuntimeProvider) installPlan(artifact RuntimeArtifact) ports.RuntimeInstallPlan {
	destination := filepath.Join(p.root, "runtimes", artifact.Component, artifact.Version+"-windows-"+artifact.Architecture)
	data, _ := json.Marshal(struct {
		WorkspaceID, Root string
		Artifact          RuntimeArtifact
	}{p.workspaceID, p.root, artifact})
	sum := sha256.Sum256(data)
	return ports.RuntimeInstallPlan{ArtifactID: artifact.ID, Component: artifact.Component, Version: artifact.Version, Architecture: artifact.Architecture, Destination: destination, Fingerprint: hex.EncodeToString(sum[:])}
}

// Install re-derives every location/source from the catalog and publishes only complete validated SDKs.
func (p *NativeRuntimeProvider) Install(ctx context.Context, plan ports.RuntimeInstallPlan) (ports.RuntimeLocation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	artifact, ok := p.artifacts[plan.ArtifactID]
	if !ok {
		return ports.RuntimeLocation{}, problem.New("RUNTIME_PLAN_STALE", "")
	}
	expected := p.installPlan(artifact)
	if plan.Fingerprint != expected.Fingerprint || plan.Destination != expected.Destination || plan.Component != expected.Component || plan.Version != expected.Version || plan.Architecture != expected.Architecture {
		return ports.RuntimeLocation{}, problem.New("RUNTIME_PLAN_STALE", "")
	}
	if existing, err := p.installed(ctx, artifact); err == nil && existing.Root != "" {
		return existing, nil
	}
	anchor, err := os.OpenRoot(p.root)
	if err != nil {
		return ports.RuntimeLocation{}, err
	}
	defer anchor.Close()
	final := filepath.ToSlash(filepath.Join("runtimes", artifact.Component, artifact.Version+"-windows-"+artifact.Architecture))
	if _, err := anchor.Lstat(final); !os.IsNotExist(err) {
		return ports.RuntimeLocation{}, problem.New("RUNTIME_INSTALL_CONFLICT", "")
	}
	cache, err := p.download(ctx, anchor, artifact)
	if err != nil {
		return ports.RuntimeLocation{}, err
	}
	nonce, err := identity.NewUUID()
	if err != nil {
		return ports.RuntimeLocation{}, err
	}
	stage := final + ".staging-" + nonce
	if err := anchor.MkdirAll(path.Dir(stage), 0700); err != nil {
		return ports.RuntimeLocation{}, err
	}
	if err := anchor.Mkdir(stage, 0700); err != nil {
		return ports.RuntimeLocation{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = anchor.RemoveAll(stage)
		}
	}()
	files, err := extractRuntime(ctx, anchor, cache, stage, artifact)
	if err != nil {
		return ports.RuntimeLocation{}, err
	}
	executable := path.Join(stage, artifact.Executable)
	info, err := anchor.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return ports.RuntimeLocation{}, problem.New("RUNTIME_EXECUTABLE_MISSING", "")
	}
	state := installedRuntime{ArtifactID: artifact.ID, ArtifactSHA256: artifact.SHA256, Fingerprint: expected.Fingerprint, Files: files}
	stateData, err := json.Marshal(state)
	if err != nil {
		return ports.RuntimeLocation{}, err
	}
	if err := writeRuntimeFile(anchor, path.Join(stage, runtimeMarker), stateData); err != nil {
		return ports.RuntimeLocation{}, err
	}
	if err := ctx.Err(); err != nil {
		return ports.RuntimeLocation{}, err
	}
	if err := anchor.Rename(stage, final); err != nil {
		return ports.RuntimeLocation{}, err
	}
	published = true
	return p.location(artifact), nil
}

type installedRuntime struct {
	ArtifactID     string        `json:"artifactId"`
	ArtifactSHA256 string        `json:"artifactSha256"`
	Fingerprint    string        `json:"fingerprint"`
	Files          []runtimeFile `json:"files"`
}

func (p *NativeRuntimeProvider) location(artifact RuntimeArtifact) ports.RuntimeLocation {
	plan := p.installPlan(artifact)
	return ports.RuntimeLocation{Root: plan.Destination, Executable: filepath.Join(plan.Destination, filepath.FromSlash(artifact.Executable)), Component: artifact.Component, Version: artifact.Version, Architecture: artifact.Architecture, ABI: artifact.ABI, Fingerprint: plan.Fingerprint}
}
func (p *NativeRuntimeProvider) installed(ctx context.Context, artifact RuntimeArtifact) (ports.RuntimeLocation, error) {
	plan := p.installPlan(artifact)
	data, err := readSmallRegularFile(filepath.Join(plan.Destination, runtimeMarker), 16*1024*1024)
	if err != nil {
		return ports.RuntimeLocation{}, err
	}
	var state installedRuntime
	if json.Unmarshal(data, &state) != nil || len(state.Files) == 0 || state.ArtifactID != artifact.ID || state.ArtifactSHA256 != artifact.SHA256 || state.Fingerprint != plan.Fingerprint {
		return ports.RuntimeLocation{}, problem.New("RUNTIME_INTEGRITY_MISMATCH", "")
	}
	anchor, err := os.OpenRoot(plan.Destination)
	if err != nil {
		return ports.RuntimeLocation{}, err
	}
	defer anchor.Close()
	hasExecutable := false
	for _, file := range state.Files {
		if err := ctx.Err(); err != nil {
			return ports.RuntimeLocation{}, err
		}
		if project.ValidateRelativePath(file.Path) != nil || len(file.SHA256) != 64 || file.Size < 0 || file.Size > maxRuntimeFile {
			return ports.RuntimeLocation{}, problem.New("RUNTIME_INTEGRITY_MISMATCH", "")
		}
		info, err := anchor.Lstat(file.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
			return ports.RuntimeLocation{}, problem.New("RUNTIME_INTEGRITY_MISMATCH", "")
		}
		stream, err := anchor.Open(file.Path)
		if err != nil {
			return ports.RuntimeLocation{}, err
		}
		digest := sha256.New()
		n, err := io.Copy(digest, io.LimitReader(contextReader{ctx, stream}, maxRuntimeFile+1))
		stream.Close()
		if err != nil {
			return ports.RuntimeLocation{}, err
		}
		if n != file.Size || hex.EncodeToString(digest.Sum(nil)) != file.SHA256 {
			return ports.RuntimeLocation{}, problem.New("RUNTIME_INTEGRITY_MISMATCH", "")
		}
		if file.Path == artifact.Executable {
			hasExecutable = true
		}
	}
	if !hasExecutable {
		return ports.RuntimeLocation{}, problem.New("RUNTIME_INTEGRITY_MISMATCH", "")
	}
	return p.location(artifact), nil
}

// CheckSourcePreparation rejects writes into original projects even when PATH could point at another Python.
func CheckSourcePreparation(source string, managed bool) error {
	if !managed {
		return problem.New("SOURCE_COPY_REQUIRED", "")
	}
	if info, err := os.Lstat(filepath.Join(source, ".deps")); err == nil && info.IsDir() {
		return problem.New("INHERITED_PYTHON_DEPENDENCIES", "")
	}
	return nil
}

func writeRuntimeFile(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
