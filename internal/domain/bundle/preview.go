package bundle

import "github.com/JeropsWang/local-project-hub/internal/domain/project"

// Inspection contains portable evidence only. Local bindings and permissions
// are resolved separately when the user registers the project.
type Inspection struct {
	Identity         string           `json:"bundleIdentity"`
	Document         project.Document `json:"document"`
	RequiredBindings []string         `json:"requiredBindings"`
	Warnings         []string         `json:"warnings"`
}
