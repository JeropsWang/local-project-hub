package environment

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

//go:embed catalog.json
var catalogData []byte

// RuntimeArtifact is a trusted supply descriptor, never decoded from a project bundle.
type RuntimeArtifact struct {
	ID           string `json:"id"`
	Component    string `json:"component"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	ABI          string `json:"abi,omitempty"`
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	Format       string `json:"format"`
	StripPrefix  string `json:"stripPrefix"`
	Executable   string `json:"executable"`
	Source       string `json:"source"`
	License      string `json:"license"`
	BundledNpm   string `json:"bundledNpm,omitempty"`
}

// RuntimeCatalog versions the manager-maintained artifact list independently of project JSON.
type RuntimeCatalog struct {
	SchemaVersion string            `json:"schemaVersion"`
	VerifiedAt    string            `json:"verifiedAt,omitempty"`
	Artifacts     []RuntimeArtifact `json:"artifacts"`
}

// DefaultRuntimeCatalog reads the pinned build-time catalog; it does not contact a server.
func DefaultRuntimeCatalog() (RuntimeCatalog, error) {
	var catalog RuntimeCatalog
	if err := json.Unmarshal(catalogData, &catalog); err != nil {
		return catalog, err
	}
	return catalog, validateCatalog(catalog)
}

var runtimeIdentifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,95}$`)
var runtimeVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func validateCatalog(catalog RuntimeCatalog) error {
	if catalog.SchemaVersion != "1.0" || len(catalog.Artifacts) == 0 {
		return problem.New("RUNTIME_CATALOG_INVALID", "")
	}
	seen := map[string]bool{}
	for _, artifact := range catalog.Artifacts {
		if !runtimeIdentifier.MatchString(artifact.ID) || seen[artifact.ID] ||
			!runtimeVersion.MatchString(artifact.Version) {
			return problem.New("RUNTIME_CATALOG_INVALID", "")
		}
		seen[artifact.ID] = true
		if artifact.Component != "node" && artifact.Component != "go" && artifact.Component != "python" {
			return problem.New("RUNTIME_CATALOG_INVALID", "")
		}
		parsed, err := url.Parse(artifact.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return problem.New("RUNTIME_SOURCE_INVALID", "")
		}
		if len(artifact.SHA256) != 64 {
			return problem.New("RUNTIME_CATALOG_HASH_INVALID", "")
		}
		if _, err := hex.DecodeString(artifact.SHA256); err != nil {
			return problem.New("RUNTIME_CATALOG_HASH_INVALID", "")
		}
		if artifact.Architecture != "amd64" || (artifact.Format != "zip" && artifact.Format != "tar.gz") {
			return problem.New("RUNTIME_CATALOG_INVALID", "")
		}
		if !strings.HasSuffix(artifact.StripPrefix, "/") ||
			project.ValidateRelativePath(strings.TrimSuffix(artifact.StripPrefix, "/")) != nil ||
			project.ValidateRelativePath(artifact.Executable) != nil {
			return problem.New("RUNTIME_CATALOG_PATH_INVALID", "")
		}
		if artifact.Source == "" || artifact.License == "" {
			return problem.New("RUNTIME_CATALOG_PROVENANCE_REQUIRED", "")
		}
	}
	return nil
}
