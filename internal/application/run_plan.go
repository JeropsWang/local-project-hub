package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func (s *PlanService) compileRun(
	ctx context.Context,
	repo ports.Repository,
	ref ProfileRef,
	doc project.Document,
	ordered []string,
	bindings map[string]project.Binding,
) (run.Snapshot, error) {
	snapshot, err := s.runCompiler.CompileRun(
		ctx,
		ports.InspectionRequest{
			Document:      doc,
			Bindings:      bindings,
			WorkspaceRoot: repo.Info().Root,
			Revision:      ref.ExpectedRevision,
			UnitIDs:       ordered,
		},
	)
	if err != nil {
		return snapshot, err
	}
	snapshot.ProfileRef = ref
	for index := range snapshot.Units {
		unit := &snapshot.Units[index]
		if unit.Kind != "compose" && unit.Kind != "external" {
			continue
		}
		store, ok := repo.(ports.ResourceRepository)
		if !ok {
			return snapshot, problem.New("RESOURCE_STORAGE_UNAVAILABLE", unit.ID)
		}
		record, err := store.Resource(ctx, unit.ResourceID)
		if err != nil {
			return snapshot, err
		}
		if record.PendingStart != "" {
			return snapshot, problem.New("RESOURCE_START_UNRESOLVED", unit.ID)
		}
		if unit.Control == "managed" && (record.Control != "managed" || record.ControlGrant == "") {
			return snapshot, problem.New("RESOURCE_CONTROL_REQUIRED", unit.ID)
		}
		unit.ResourceFingerprint = record.Fingerprint
	}
	return snapshot, nil
}

// Compiled input comparison excludes allocation IDs/timestamps and retains all
// resolved commands, script bytes, resource authority and environment selections.
func compiledRunFingerprint(snapshot run.Snapshot) string {
	snapshot.ID = ""
	snapshot.CreatedAt = run.Snapshot{}.CreatedAt
	snapshot.Units = append([]run.UnitPlan{}, snapshot.Units...)
	for index := range snapshot.Units {
		snapshot.Units[index].InstanceID = ""
	}
	data, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
