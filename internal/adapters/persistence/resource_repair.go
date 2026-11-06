package persistence

import (
	"context"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
)

// ClearSettledStart is the commit step of an explicit, read-only repair.
func (s *Store) ClearSettledStart(ctx context.Context, id, fingerprint, operation string) error {
	return s.mutateResource(ctx, id, func(record *resource.Record) error {
		if s.info.State != "ready" || record.ControlGrant == "" || record.Fingerprint != fingerprint ||
			operation == "" || record.PendingStart != operation || len(record.Users) != 0 {
			return problem.New("RESOURCE_REPAIR_CONFLICT", id)
		}
		record.ReconciledStart = record.PendingStart
		record.PendingStart = ""
		record.Active = false
		record.Fingerprint = resource.Fingerprint(*record)
		return nil
	})
}
