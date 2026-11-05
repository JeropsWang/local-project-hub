package ports

import (
	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

// VersionEvaluator compares ecosystem-specific constraints with observed metadata.
type VersionEvaluator func(project.Requirement, Observation) (check.Item, error)
