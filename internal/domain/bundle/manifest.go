// Package bundle defines the portable configuration package without host state.
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	Format       = "local-project-hub/config"
	Version      = "1.0"
	ProjectFile  = "project.json"
	ManifestFile = "manifest.json"
	MaxArchive   = 64 << 20
	MaxExpanded  = 128 << 20
	MaxFile      = 16 << 20
	MaxManifest  = 1 << 20
	MaxFiles     = 2048
)

type Entry struct {
	Path   string `json:"path"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Format              string   `json:"format"`
	BundleVersion       string   `json:"bundleVersion"`
	MinManagerVersion   string   `json:"minManagerVersion"`
	ProjectID           string   `json:"projectId"`
	ProjectFile         string   `json:"projectFile"`
	Files               []Entry  `json:"files"`
	ExternalBindingKeys []string `json:"externalBindingKeys"`
	CreatedAt           string   `json:"createdAt"`
}

type ExportInput struct {
	Destination string
	Project     []byte
	Files       map[string][]byte
}

type ExportResult struct {
	Manifest Manifest
	Warnings []string
}

// Digest hashes original bytes rather than their parsed representation.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
