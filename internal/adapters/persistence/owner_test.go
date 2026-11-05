package persistence

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func TestUnverifiableWorkspaceOwnerCannotCarryControlGrants(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := NewFactory().Initialize(ctx, root, "owner")
	if err != nil {
		t.Fatal(err)
	}
	id := repo.Info().WorkspaceID
	resourceID := "22222222-2222-4222-8222-222222222222"
	_, err = repo.(ports.ResourceRepository).SaveResource(
		ctx,
		resource.Record{
			SchemaVersion: "1.0",
			ResourceID:    resourceID,
			Control:       "managed",
			ControlGrant:  "33333333-3333-4333-8333-333333333333",
			Engine:        "desktop",
			EngineID:      "fixture-engine",
			Active:        true,
		},
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := run.Snapshot{
		ID: "44444444-4444-4444-8444-444444444444",
		ProfileRef: project.Ref{
			ProjectID:        "55555555-5555-4555-8555-555555555555",
			ProfileID:        "development",
			ExpectedRevision: 1,
		},
		CreatedAt: time.Now().UTC(),
	}
	if err = repo.(ports.RunRepository).CreateRun(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = repo.Close(); err != nil {
		t.Fatal(err)
	}
	// An encrypted proof from another machine/user is unverifiable in the same
	// way as this synthetic invalid blob. Keep the path and portable identity.
	if err = os.WriteFile(filepath.Join(root, ".local-owner.dpapi"), []byte("synthetic foreign-owner ciphertext"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err = NewFactory().Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if repo.Info().State != "rebind-required" {
		t.Fatal("same-path copy retained machine authority")
	}
	existing, err := repo.(ports.ResourceRepository).Resource(ctx, resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.(ports.ResourceRepository).SaveResource(ctx, existing, existing.Fingerprint); err == nil ||
		err.Error() != "WORKSPACE_REBIND_REQUIRED" {
		t.Fatal("unverified host admitted a grant", err)
	}
	if _, err = repo.ConfirmRelocation(ctx, id); err != nil {
		t.Fatal("foreign records prevent target rebind", err)
	}
	resources, err := repo.(ports.ResourceRepository).ListResources(ctx)
	if err != nil || len(resources) != 0 {
		t.Fatal("inherited grants", err)
	}
	runs, err := repo.(ports.RunRepository).ActiveRuns(ctx)
	if err != nil || len(runs) != 0 {
		t.Fatal("inherited active runs", err)
	}
	archived, err := repo.(ports.RunRepository).Run(ctx, snapshot.ID)
	if err != nil || archived.State != "detached" {
		t.Fatal("lost uncertain origin evidence", err)
	}
	if repo.Info().State != "ready" {
		t.Fatal("target rebind incomplete")
	}
	if err = repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = NewFactory().Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Info().State != "ready" {
		t.Fatal("new local proof did not persist")
	}
	if err = repo.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFailedOwnerProofPublicationKeepsOldGrantsRevoked(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := NewFactory().Initialize(ctx, root, "proof fault")
	if err != nil {
		t.Fatal(err)
	}
	id := repo.Info().WorkspaceID
	if _, err = repo.(ports.ResourceRepository).SaveResource(ctx, resource.Record{SchemaVersion: "1.0", ResourceID: "22222222-2222-4222-8222-222222222222", Control: "managed", ControlGrant: "33333333-3333-4333-8333-333333333333", Engine: "desktop", EngineID: "fixture-engine"}, ""); err != nil {
		t.Fatal(err)
	}
	if err = repo.Close(); err != nil {
		t.Fatal(err)
	}
	proof := filepath.Join(root, ".local-owner.dpapi")
	if err = os.WriteFile(proof, []byte("synthetic foreign proof"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err = NewFactory().Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	// Replace only this test-owned file with an empty directory after opening,
	// forcing proof publication to fail after the invalidation transaction.
	if err = os.Remove(proof); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(proof, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ConfirmRelocation(ctx, id); err == nil {
		t.Fatal("published readiness without an owner proof")
	}
	if repo.Info().State != "rebind-required" {
		t.Fatal("half-committed host change became ready")
	}
	records, err := repo.(ports.ResourceRepository).ListResources(ctx)
	if err != nil || len(records) != 0 {
		t.Fatal("old grants survived failed proof publication", err)
	}
	if err = os.Remove(proof); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ConfirmRelocation(ctx, id); err != nil {
		t.Fatal("repair did not converge", err)
	}
}
