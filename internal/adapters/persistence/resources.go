package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type resourceQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readResource(ctx context.Context, query resourceQuery, id string) (resource.Record, error) {
	var result resource.Record
	var data []byte
	var grant, fingerprint string
	if err := query.QueryRowContext(ctx, "SELECT locator_json,control_grant,fingerprint FROM resources WHERE resource_id=?", id).Scan(&data, &grant, &fingerprint); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, problem.New("RESOURCE_MISSING", id)
		}
		return result, err
	}
	if len(data) > 16*1024*1024 || json.Unmarshal(data, &result) != nil || result.SchemaVersion != "1.0" ||
		result.ResourceID != id {
		return result, problem.New("RESOURCE_VERSION_UNSUPPORTED", id)
	}
	result.ControlGrant = grant
	result.Fingerprint = fingerprint
	if result.StopAttempts == nil {
		result.StopAttempts = map[string]string{}
	}
	return result, nil
}

// Resource reads only the workspace's local authority column, never a copied payload grant.
func (s *Store) Resource(ctx context.Context, id string) (resource.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readResource(ctx, s.db, id)
}

// ListResources returns the bounded local registry for project/container relationship views.
func (s *Store) ListResources(ctx context.Context) ([]resource.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, "SELECT resource_id FROM resources ORDER BY resource_id LIMIT 1001")
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > 1000 {
		return nil, problem.New("RESOURCE_LIMIT", "")
	}
	result := []resource.Record{}
	for _, id := range ids {
		record, err := readResource(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

// SaveResource replaces only an unused explicitly reviewed binding, using optimistic identity.
func (s *Store) SaveResource(ctx context.Context, record resource.Record, expected string) (resource.Record, error) {
	if err := resource.Validate(record); err != nil {
		return resource.Record{}, err
	}
	if len(record.Users) != 0 {
		return resource.Record{}, problem.New("RESOURCE_USE_INVALID", "")
	}
	record.Fingerprint = resource.Fingerprint(record)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.State != "ready" {
		return resource.Record{}, problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return resource.Record{}, err
	}
	defer tx.Rollback()
	previous, readErr := readResource(ctx, tx, record.ResourceID)
	if readErr != nil && readErr.Error() != "RESOURCE_MISSING" {
		return resource.Record{}, readErr
	}
	if readErr == nil && previous.Fingerprint != expected || readErr != nil && expected != "" {
		return resource.Record{}, problem.New("RESOURCE_BINDING_CONFLICT", record.ResourceID)
	}
	if len(previous.Users) > 0 {
		return resource.Record{}, problem.New("RESOURCE_IN_USE", record.ResourceID)
	}
	if previous.PendingStart != "" {
		return resource.Record{}, problem.New("RESOURCE_START_PENDING", record.ResourceID)
	}
	if record.ControlGrant != "" {
		var conflict bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM resources WHERE control_grant=? AND resource_id<>?)", record.ControlGrant, record.ResourceID).Scan(&conflict); err != nil {
			return resource.Record{}, err
		}
		if conflict {
			return resource.Record{}, problem.New("RESOURCE_GRANT_CONFLICT", "")
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return resource.Record{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO resources(resource_id,locator_json,control_grant,fingerprint) VALUES(?,?,?,?) ON CONFLICT(resource_id) DO UPDATE SET locator_json=excluded.locator_json,control_grant=excluded.control_grant,fingerprint=excluded.fingerprint`, record.ResourceID, data, record.ControlGrant, record.Fingerprint); err != nil {
		return resource.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return resource.Record{}, err
	}
	return record, nil
}

// AcquireResource atomically adds one exact run/unit, keeping repeat submissions idempotent.
func (s *Store) AcquireResource(ctx context.Context, id, fingerprint string, use resource.Use) error {
	if err := resource.ValidateUse(use); err != nil {
		return err
	}
	return s.mutateResource(ctx, id, func(record *resource.Record) error {
		if record.Fingerprint == "" || record.Fingerprint != fingerprint {
			return problem.New("RESOURCE_BINDING_CONFLICT", id)
		}
		for _, existing := range record.Users {
			if existing.RunID == use.RunID && existing.UnitID == use.UnitID {
				if existing != use {
					return problem.New("RESOURCE_USE_CONFLICT", id)
				}
				return nil
			}
		}
		if len(record.Users) >= 4096 {
			return problem.New("RESOURCE_USE_LIMIT", id)
		}
		record.Users = append(record.Users, use)
		return nil
	})
}

// ReleaseResource removes only this stopped run's reference, never another consumer.
func (s *Store) ReleaseResource(ctx context.Context, id string, use resource.Use) error {
	if err := resource.ValidateUse(use); err != nil {
		return err
	}
	return s.mutateResource(ctx, id, func(record *resource.Record) error {
		record.Users = slices.DeleteFunc(record.Users, func(existing resource.Use) bool { return existing == use })
		return nil
	})
}

// MarkResourceStopped clears workspace liveness only after a fully observed
// normal stop and a fresh check that no consumer/binding replaced this resource.
func (s *Store) MarkResourceStopped(ctx context.Context, id, fingerprint string) error {
	return s.mutateResource(ctx, id, func(record *resource.Record) error {
		if record.Fingerprint != fingerprint || record.PendingStart != "" || len(record.Users) > 0 {
			return problem.New("RESOURCE_BINDING_CONFLICT", id)
		}
		record.Active = false
		return nil
	})
}

func (s *Store) mutateResource(ctx context.Context, id string, change func(*resource.Record) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	record, err := readResource(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := change(&record); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE resources SET locator_json=?,fingerprint=? WHERE resource_id=?", data, record.Fingerprint, id); err != nil {
		return err
	}
	return tx.Commit()
}

func authorizedContainer(ctx context.Context, query resourceQuery, instance run.Identity) (resource.Record, error) {
	var id string
	if instance.ControlID == "" ||
		query.QueryRowContext(ctx, "SELECT resource_id FROM resources WHERE control_grant=? AND control_grant<>''", instance.ControlID).
			Scan(&id) !=
			nil {
		return resource.Record{}, problem.New("RESOURCE_CONTROL_NOT_GRANTED", "")
	}
	record, err := readResource(ctx, query, id)
	if err != nil {
		return record, err
	}
	if record.Control != "managed" || record.Fingerprint == "" || !slices.Contains(record.Instances, instance) {
		return record, problem.New("RESOURCE_CONTROL_NOT_GRANTED", id)
	}
	return record, nil
}

// AuthorizeContainer requires the exact local engine/start identity in a separately granted binding.
func (s *Store) AuthorizeContainer(ctx context.Context, instance run.Identity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.State != "ready" {
		return problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	_, err := authorizedContainer(ctx, s.db, instance)
	return err
}

// BeginContainerStop durably records attempted delivery before any Engine effect.
func (s *Store) BeginContainerStop(ctx context.Context, instance run.Identity, signal string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.State != "ready" {
		return false, problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	record, err := authorizedContainer(ctx, tx, instance)
	if err != nil {
		return false, err
	}
	key := instance.ContainerID + "@" + instance.StartedAt
	if prior, exists := record.StopAttempts[key]; exists {
		if prior != signal {
			return false, problem.New("CONTAINER_STOP_SIGNAL_CHANGED", "")
		}
		return false, nil
	}
	record.StopAttempts[key] = signal
	data, err := json.Marshal(record)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE resources SET locator_json=? WHERE resource_id=?", data, record.ResourceID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// AuthorizeCompose requires an already reviewed local source/engine grant.
func (s *Store) AuthorizeCompose(ctx context.Context, spec ports.ComposeSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.State != "ready" {
		return problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	record, err := readResource(ctx, s.db, spec.ResourceID)
	if err != nil {
		return err
	}
	if record.Control != "managed" || record.ControlGrant == "" || record.ControlGrant != spec.ControlGrant ||
		record.Engine != spec.Engine ||
		record.EngineID != spec.ExpectedEngineID ||
		record.ComposeProject != spec.ProjectName ||
		record.WorkingDir != spec.WorkingDir ||
		record.ConfigFingerprint != spec.ConfigFingerprint ||
		len(record.Files) != len(spec.Files) ||
		!slices.Equal(record.Services, spec.Services) {
		return problem.New("RESOURCE_CONTROL_NOT_GRANTED", spec.ResourceID)
	}
	for i, file := range record.Files {
		if file.Path != spec.Files[i].Path || file.SHA256 != spec.Files[i].SHA256 {
			return problem.New("RESOURCE_BINDING_CONFLICT", spec.ResourceID)
		}
	}
	return nil
}

// BeginCompose records a unique intent before an external CLI can create anything.
func (s *Store) BeginCompose(ctx context.Context, spec ports.ComposeSpec, attempt string) error {
	if err := s.AuthorizeCompose(ctx, spec); err != nil {
		return err
	}
	return s.mutateResource(ctx, spec.ResourceID, func(record *resource.Record) error {
		if record.ControlGrant != spec.ControlGrant || record.ConfigFingerprint != spec.ConfigFingerprint {
			return problem.New("RESOURCE_BINDING_CONFLICT", spec.ResourceID)
		}
		if record.PendingStart != "" {
			return problem.New("RESOURCE_START_PENDING", spec.ResourceID)
		}
		record.PendingStart = attempt
		record.Active = true
		return nil
	})
}

// RecordComposeInstances retains acquisitions even if the CLI reports a partial failure.
func (s *Store) RecordComposeInstances(
	ctx context.Context,
	spec ports.ComposeSpec,
	attempt string,
	instances []run.Identity,
	complete bool,
) error {
	if err := s.AuthorizeCompose(ctx, spec); err != nil {
		return err
	}
	return s.mutateResource(ctx, spec.ResourceID, func(record *resource.Record) error {
		if record.ControlGrant != spec.ControlGrant || record.PendingStart != attempt {
			return problem.New("RESOURCE_BINDING_CONFLICT", spec.ResourceID)
		}
		for _, instance := range instances {
			if instance.Kind != "container" || instance.ControlID != record.ControlGrant ||
				instance.Engine != record.Engine ||
				instance.EngineID != record.EngineID {
				return problem.New("CONTAINER_IDENTITY_INVALID", "")
			}
			record.Instances = slices.DeleteFunc(
				record.Instances,
				func(prior run.Identity) bool { return prior.ContainerID == instance.ContainerID },
			)
			record.Instances = append(record.Instances, instance)
		}
		if complete {
			record.PendingStart = ""
			record.Active = len(instances) > 0
		}
		record.Fingerprint = resource.Fingerprint(*record)
		return nil
	})
}
