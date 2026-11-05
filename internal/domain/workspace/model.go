// Package workspace models local workspace identity and relocation state.
package workspace

import "time"

// Document is the only portable workspace identity file.
type Document struct {
	SchemaVersion string    `json:"schemaVersion"`
	WorkspaceID   string    `json:"workspaceId"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"createdAt"`
}

// View contains local-only location and schema information.
type View struct {
	WorkspaceID     string `json:"workspaceId"`
	Name            string `json:"name"`
	Root            string `json:"root"`
	State           string `json:"state"`
	DBSchemaVersion int    `json:"dbSchemaVersion"`
}
