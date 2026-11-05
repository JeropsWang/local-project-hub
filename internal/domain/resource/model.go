// Package resource models local resource bindings and their current consumers.
// A portable resource UUID identifies a relationship; it is never a control grant.
package resource

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var key = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// File is an ordered local Compose input, excluded from portable host bindings.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Use fixes one run's resource acquisition; retries of the same use are idempotent.
type Use struct {
	ProjectID string `json:"projectId"`
	RunID     string `json:"runId"`
	UnitID    string `json:"unitId"`
}

// Record is workspace-local. ControlGrant is stored separately so relocation can
// revoke it without making stale JSON payloads an alternative source of authority.
type Record struct {
	SchemaVersion         string            `json:"schemaVersion"`
	ResourceID            string            `json:"resourceId"`
	Control               string            `json:"control"`
	Engine                string            `json:"engine"`
	EngineID              string            `json:"engineId"`
	ComposeProject        string            `json:"composeProject"`
	WorkingDir            string            `json:"workingDir"`
	Files                 []File            `json:"files"`
	Services              []string          `json:"services"`
	ConfigFingerprint     string            `json:"configFingerprint"`
	ContainerConfigHashes map[string]string `json:"containerConfigHashes"`
	Instances             []run.Identity    `json:"instances"`
	Shared                bool              `json:"shared"`
	Active                bool              `json:"active"`
	ControlGrant          string            `json:"-"`
	Fingerprint           string            `json:"fingerprint"`
	Users                 []Use             `json:"users"`
	StopAttempts          map[string]string `json:"stopAttempts"`
	PendingStart          string            `json:"pendingStart,omitempty"`
}

// Validate rejects malformed local records before storage, without inventing authority.
func Validate(record Record) error {
	if record.SchemaVersion != "1.0" || !uuid.MatchString(record.ResourceID) || !key.MatchString(record.Engine) ||
		record.EngineID == "" ||
		len(record.EngineID) > 256 ||
		len(record.Instances) > 10000 ||
		len(record.Users) > 4096 {
		return problem.New("RESOURCE_INVALID", "")
	}
	if record.Control != "reference" && record.Control != "managed" {
		return problem.New("RESOURCE_CONTROL_INVALID", "")
	}
	if record.Control == "reference" && record.ControlGrant != "" ||
		record.Control == "managed" && !uuid.MatchString(record.ControlGrant) {
		return problem.New("RESOURCE_CONTROL_INVALID", "")
	}
	for _, use := range record.Users {
		if err := ValidateUse(use); err != nil {
			return err
		}
	}
	return nil
}

// ValidateUse prevents accidental crossing of project, run and unit identities.
func ValidateUse(use Use) error {
	if !uuid.MatchString(use.ProjectID) || !uuid.MatchString(use.RunID) || !key.MatchString(use.UnitID) {
		return problem.New("RESOURCE_USE_INVALID", "")
	}
	return nil
}

// Fingerprint excludes mutable consumers/stop journals but includes the local grant
// and instance identities, so a changed binding cannot silently reuse an old plan.
func Fingerprint(record Record) string {
	record.Fingerprint = ""
	record.Users = nil
	record.StopAttempts = nil
	record.PendingStart = ""
	record.Active = false
	data, _ := json.Marshal(struct {
		Record Record
		Grant  string
	}{record, record.ControlGrant})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ComposeProject fixes new stack naming independently of project folder names.
func ComposeProject(resourceID string) (string, error) {
	if !uuid.MatchString(resourceID) {
		return "", problem.New("RESOURCE_ID_INVALID", "")
	}
	return "lph-" + strings.ReplaceAll(resourceID, "-", ""), nil
}
