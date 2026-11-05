package application_test

import (
	"context"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

const resourceFixtureID = "22222222-2222-4222-8222-222222222222"
const resourceFixtureContainer = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type resourceContainers struct {
	inventory []ports.ContainerObservation
	stops     int
}

func (c *resourceContainers) Inventory(context.Context, string) ([]ports.ContainerObservation, error) {
	return c.inventory, nil
}
func (c *resourceContainers) Start(context.Context, ports.ComposeSpec) ([]run.Identity, error) {
	return nil, nil
}
func (c *resourceContainers) Stop(context.Context, run.Identity) (ports.ContainerStopResult, error) {
	c.stops++
	return ports.ContainerStopResult{Graceful: true}, nil
}
func (c *resourceContainers) PreviewCompose(ctx context.Context, spec ports.ComposeSpec) (ports.ComposePreview, error) {
	spec.ConfigFingerprint = "fixture-source-config"
	return ports.ComposePreview{Spec: spec, ProjectName: spec.ProjectName, Fingerprint: spec.ConfigFingerprint}, nil
}

func referenceObservation() ports.ContainerObservation {
	return ports.ContainerObservation{
		Identity: run.Identity{
			Kind:         "container",
			Engine:       "desktop",
			EngineID:     "fixture-engine",
			ContainerID:  resourceFixtureContainer,
			CreationTime: "created",
			StartedAt:    "started",
		},
		ComposeProject: "existing-stack",
		ComposeService: "database",
		ComposeFiles:   `D:\fixture\base.yml`,
		WorkingDir:     `D:\fixture`,
		ConfigHash:     "actual-container-config",
		Running:        true,
	}
}

func TestResourceUUIDAndReferenceDoNotGrantStopAuthority(t *testing.T) {
	ws, _ := services(t)
	containers := &resourceContainers{inventory: []ports.ContainerObservation{referenceObservation()}}
	resources := application.NewResourceService(ws, containers, containers)
	ctx := context.Background()
	view, err := resources.RegisterReference(
		ctx,
		application.ReferenceResourceRequest{
			ResourceID:   resourceFixtureID,
			Engine:       "desktop",
			ContainerIDs: []string{resourceFixtureContainer},
		},
	)
	if err != nil || view.ControlGrant != "" || view.Control != "reference" {
		t.Fatalf("reference acquired grant: %+v %v", view, err)
	}
	result, err := resources.StopUnused(ctx, resourceFixtureID, true)
	if err != nil || result.Reason != "reference" || containers.stops != 0 {
		t.Fatalf("UUID/reference allowed control: %+v %v", result, err)
	}
}

func TestSharedResourceNeedsGrantAndLastUserBeforeGlobalStop(t *testing.T) {
	ws, _ := services(t)
	containers := &resourceContainers{inventory: []ports.ContainerObservation{referenceObservation()}}
	resources := application.NewResourceService(ws, containers, containers)
	ctx := context.Background()
	spec := ports.ComposeSpec{
		ResourceID:       resourceFixtureID,
		Engine:           "desktop",
		ExpectedEngineID: "fixture-engine",
		ProjectName:      "existing-stack",
		WorkingDir:       `D:\fixture`,
		Files:            []ports.ComposeFile{{Path: `D:\fixture\base.yml`, SHA256: "fixture-file"}},
		Services:         []string{"database"},
	}
	view, err := resources.GrantManaged(
		ctx,
		application.ManagedResourceRequest{Spec: spec, Shared: true, AdoptExisting: true},
	)
	if err != nil || view.ControlGrant == "" {
		t.Fatalf("explicit grant: %+v %v", view, err)
	}
	users := []resource.Use{
		{
			ProjectID: "11111111-1111-4111-8111-111111111111",
			RunID:     "55555555-5555-4555-8555-555555555555",
			UnitID:    "database",
		},
		{
			ProjectID: "44444444-4444-4444-8444-444444444444",
			RunID:     "66666666-6666-4666-8666-666666666666",
			UnitID:    "database",
		},
	}
	for _, user := range users {
		if err := resources.Acquire(ctx, resourceFixtureID, view.Fingerprint, user); err != nil {
			t.Fatal(err)
		}
	}
	if err := resources.Release(ctx, resourceFixtureID, users[0]); err != nil {
		t.Fatal(err)
	}
	result, err := resources.StopUnused(ctx, resourceFixtureID, true)
	if err != nil || result.Reason != "in-use" || containers.stops != 0 {
		t.Fatal("stopped another project's resource", err)
	}
	if err := resources.Release(ctx, resourceFixtureID, users[1]); err != nil {
		t.Fatal(err)
	}
	result, err = resources.StopUnused(ctx, resourceFixtureID, false)
	if err != nil || result.Reason != "shared-kept-running" || containers.stops != 0 {
		t.Fatal("individual stop took down shared resource", err)
	}
	result, err = resources.StopUnused(ctx, resourceFixtureID, true)
	if err != nil || !result.Stopped || containers.stops != 1 {
		t.Fatalf("unused granted shared resource not stopped: %+v %v", result, err)
	}
	view, err = resources.Get(ctx, resourceFixtureID)
	if err != nil || len(view.Users) != 0 {
		t.Fatal("reference count lost", err)
	}
}

func TestExistingComposeSourceMismatchNeverCreatesGrant(t *testing.T) {
	ws, _ := services(t)
	observed := referenceObservation()
	observed.WorkingDir = `D:\different-checkout`
	containers := &resourceContainers{inventory: []ports.ContainerObservation{observed}}
	resources := application.NewResourceService(ws, containers, containers)
	spec := ports.ComposeSpec{
		ResourceID:       resourceFixtureID,
		Engine:           "desktop",
		ExpectedEngineID: "fixture-engine",
		ProjectName:      "existing-stack",
		WorkingDir:       `D:\fixture`,
		Files:            []ports.ComposeFile{{Path: `D:\fixture\base.yml`}},
		Services:         []string{"database"},
	}
	_, err := resources.GrantManaged(
		context.Background(),
		application.ManagedResourceRequest{Spec: spec, AdoptExisting: true},
	)
	if err == nil || err.Error() != "RESOURCE_SOURCE_MISMATCH" {
		t.Fatalf("wrong checkout adopted: %v", err)
	}
	if _, err := resources.Get(context.Background(), resourceFixtureID); err == nil ||
		err.Error() != "RESOURCE_MISSING" {
		t.Fatal("failed adoption published grant")
	}
}

func TestNewGrantCannotSilentlyJoinAnExistingComposeNamespace(t *testing.T) {
	ws, _ := services(t)
	observation := referenceObservation()
	observation.ComposeService = "unselected-cache"
	containers := &resourceContainers{inventory: []ports.ContainerObservation{observation}}
	resources := application.NewResourceService(ws, containers, containers)
	spec := ports.ComposeSpec{
		ResourceID:       resourceFixtureID,
		Engine:           "desktop",
		ExpectedEngineID: "fixture-engine",
		ProjectName:      "existing-stack",
		WorkingDir:       `D:\fixture`,
		Files:            []ports.ComposeFile{{Path: `D:\fixture\base.yml`}},
		Services:         []string{"database"},
	}
	_, err := resources.GrantManaged(context.Background(), application.ManagedResourceRequest{Spec: spec})
	if err == nil || err.Error() != "RESOURCE_EXISTING_REQUIRES_REVIEW" {
		t.Fatalf("existing namespace silently reused: %v", err)
	}
}

func TestRunningSharedResourceKeepsWorkspaceOpenAfterLastProjectStops(t *testing.T) {
	ws, _ := services(t)
	containers := &resourceContainers{inventory: []ports.ContainerObservation{referenceObservation()}}
	resources := application.NewResourceService(ws, containers, containers)
	spec := ports.ComposeSpec{
		ResourceID:       resourceFixtureID,
		Engine:           "desktop",
		ExpectedEngineID: "fixture-engine",
		ProjectName:      "existing-stack",
		WorkingDir:       `D:\fixture`,
		Files:            []ports.ComposeFile{{Path: `D:\fixture\base.yml`}},
		Services:         []string{"database"},
	}
	if _, err := resources.GrantManaged(context.Background(), application.ManagedResourceRequest{Spec: spec, Shared: true, AdoptExisting: true}); err != nil {
		t.Fatal(err)
	}
	if err := ws.Close(); err == nil || err.Error() != "WORKSPACE_BUSY" {
		t.Fatalf("running shared resource lost its workspace: %v", err)
	}
	if result, err := resources.StopUnused(context.Background(), resourceFixtureID, true); err != nil ||
		!result.Stopped {
		t.Fatal("shared stop", err)
	}
	if err := ws.Close(); err != nil {
		t.Fatal("fully stopped workspace stayed locked", err)
	}
}
