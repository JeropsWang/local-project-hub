// Package preparation models local, disposable project environments, not portable certification.
package preparation

import "github.com/JeropsWang/local-project-hub/internal/domain/project"

// File fixes the actual bytes selected for the managed source copy.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Source retains the original locator locally so that activation is reversible.
type Source struct {
	Binding     project.Binding `json:"binding"`
	Files       []File          `json:"files"`
	Directories []string        `json:"directories"`
}

// Restore is an explicit package-manager operation, never an arbitrary setup script.
type Restore struct {
	RequirementID string       `json:"requirementId"`
	Tool          string       `json:"tool"`
	Args          []string     `json:"args"`
	Directory     project.Path `json:"directory"`
	Effect        string       `json:"effect"`
}

// Plan freezes source inventories and the reviewed install effects before preparation.
type Plan struct {
	WorkspaceID             string            `json:"workspaceId"`
	Ref                     project.Ref       `json:"profileRef"`
	Sources                 []Source          `json:"sources"`
	Restores                []Restore         `json:"restores"`
	RuntimeFingerprints     map[string]string `json:"runtimeFingerprints"`
	PreviousRuntimeBindings []project.Binding `json:"previousRuntimeBindings"`
	Warnings                []string          `json:"warnings"`
	Fingerprint             string            `json:"fingerprint"`
}

// Receipt becomes ready only after every copy and restore operation succeeds.
// Its local original bindings must not be included in a portable export.
type Receipt struct {
	WorkspaceID      string            `json:"workspaceId"`
	Ref              project.Ref       `json:"profileRef"`
	Root             string            `json:"root"`
	State            string            `json:"state"`
	PlanFingerprint  string            `json:"planFingerprint"`
	Bindings         []project.Binding `json:"bindings"`
	OriginalBindings []project.Binding `json:"originalBindings"`
}
