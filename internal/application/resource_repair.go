package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// PendingRepairPreview pins the observations shown before explicit confirmation.
type PendingRepairPreview struct {
	ResourceID  string
	OperationID string
	Fingerprint string
	Evidence    string
}

type repairEngineVerifier interface {
	VerifyEngine(context.Context, string, string) error
}

func (s *ResourceService) PreviewPendingRepair(ctx context.Context, id string) (PendingRepairPreview, error) {
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo, err := s.readyRepository()
	if err != nil {
		return PendingRepairPreview{}, err
	}
	return s.pendingRepairEvidence(ctx, repo, id)
}

// RepairPendingStart requires the operator to verify that the controller and
// Docker operation have settled. Inventory alone cannot prove no effect remains
// in flight. The repair updates the local journal and issues no Docker writes.
func (s *ResourceService) RepairPendingStart(
	ctx context.Context,
	reviewed PendingRepairPreview,
	confirmedSettled bool,
) error {
	if !confirmedSettled {
		return problem.New("RESOURCE_REPAIR_CONFIRMATION_REQUIRED", "")
	}
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo, err := s.readyRepository()
	if err != nil {
		return err
	}
	fresh, err := s.pendingRepairEvidence(ctx, repo, reviewed.ResourceID)
	if err != nil {
		return err
	}
	if fresh != reviewed {
		return problem.New("RESOURCE_REPAIR_STALE", reviewed.ResourceID)
	}
	return repo.ClearSettledStart(ctx, fresh.ResourceID, fresh.Fingerprint, fresh.OperationID)
}

func (s *ResourceService) pendingRepairEvidence(
	ctx context.Context,
	repo ports.ResourceRepository,
	id string,
) (PendingRepairPreview, error) {
	if controller, ok := s.containers.(interface{ PendingControllerActive(string) bool }); ok &&
		controller.PendingControllerActive(id) {
		return PendingRepairPreview{}, problem.New("RESOURCE_CONTROLLER_ACTIVE", id)
	}
	record, err := repo.Resource(ctx, id)
	if err != nil {
		return PendingRepairPreview{}, err
	}
	if record.PendingStart == "" || record.ControlGrant == "" || len(record.Users) != 0 {
		return PendingRepairPreview{}, problem.New("RESOURCE_REPAIR_CONFLICT", id)
	}
	verifier, ok := s.containers.(repairEngineVerifier)
	if !ok {
		return PendingRepairPreview{}, problem.New("RESOURCE_ENGINE_UNVERIFIED", id)
	}
	if err = verifier.VerifyEngine(ctx, record.Engine, record.EngineID); err != nil {
		return PendingRepairPreview{}, err
	}
	observed, err := s.containers.Inventory(ctx, record.Engine)
	if err != nil {
		return PendingRepairPreview{}, err
	}
	evidence := []ports.ContainerObservation{}
	for _, item := range observed {
		known := slices.ContainsFunc(record.Instances, func(instance run.Identity) bool {
			return instance.ContainerID == item.Identity.ContainerID &&
				instance.CreationTime == item.Identity.CreationTime
		})
		selected := record.ComposeProject != "" && item.ComposeProject == record.ComposeProject &&
			slices.Contains(record.Services, item.ComposeService)
		if !known && !selected {
			continue
		}
		claimed := item.ManagerResourceID == id && item.ManagerGrantID == record.ControlGrant &&
			item.ManagerOperationID == record.PendingStart
		if item.Identity.EngineID != record.EngineID || (!known && !claimed) || item.Running ||
			(item.Status != "exited" && item.Status != "created") {
			return PendingRepairPreview{}, problem.New("RESOURCE_REPAIR_RESIDUAL_UNVERIFIED", id)
		}
		evidence = append(evidence, item)
	}
	sort.Slice(
		evidence,
		func(i, j int) bool { return evidence[i].Identity.ContainerID < evidence[j].Identity.ContainerID },
	)
	data, _ := json.Marshal(evidence)
	digest := sha256.Sum256(data)
	return PendingRepairPreview{
		ResourceID:  id,
		OperationID: record.PendingStart,
		Fingerprint: record.Fingerprint,
		Evidence:    hex.EncodeToString(digest[:]),
	}, nil
}
