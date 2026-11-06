package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
)

// Each physical instance/service has one consumer set. Engine aliases and new
// random grant IDs cannot split that set into independently stoppable resources.
// Run this in the same transaction as both initial binding and later discovery.
func uniquePhysicalResource(ctx context.Context, tx *sql.Tx, record resource.Record) error {
	rows, err := tx.QueryContext(ctx, "SELECT locator_json FROM resources WHERE resource_id<>?", record.ResourceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return err
		}
		var other resource.Record
		if json.Unmarshal(data, &other) != nil || other.SchemaVersion != "1.0" {
			return problem.New("RESOURCE_VERSION_UNSUPPORTED", "")
		}
		if other.EngineID != record.EngineID {
			continue
		}
		for _, instance := range record.Instances {
			for _, prior := range other.Instances {
				if instance.ContainerID != "" && instance.ContainerID == prior.ContainerID {
					return problem.New("RESOURCE_PHYSICAL_CONFLICT", other.ResourceID)
				}
			}
		}
		if record.ComposeProject != "" && record.ComposeProject == other.ComposeProject {
			for _, service := range record.Services {
				if slices.Contains(other.Services, service) {
					return problem.New("RESOURCE_PHYSICAL_CONFLICT", other.ResourceID)
				}
			}
		}
	}
	return rows.Err()
}
