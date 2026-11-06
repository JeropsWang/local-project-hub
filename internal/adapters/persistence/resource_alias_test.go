package persistence

import (
	"context"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func TestDistinctResourceIDsCannotSplitPhysicalConsumers(t *testing.T) {
	for _, variant := range []string{"same-container", "overlapping-services", "reference-alias"} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			repo, err := NewFactory().Initialize(ctx, t.TempDir(), "aliases")
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			store := repo.(ports.ResourceRepository)
			first := resource.Record{
				SchemaVersion:  "1.0",
				ResourceID:     "11111111-1111-4111-8111-111111111111",
				Control:        "managed",
				ControlGrant:   "22222222-2222-4222-8222-222222222222",
				Engine:         "desktop",
				EngineID:       "engine",
				ComposeProject: "stack",
				Services:       []string{"database"},
				Instances:      []run.Identity{{Kind: "container", ContainerID: "original"}},
			}
			if _, err = store.SaveResource(ctx, first, ""); err != nil {
				t.Fatal(err)
			}
			second := first
			second.ResourceID = "33333333-3333-4333-8333-333333333333"
			second.ControlGrant = "44444444-4444-4444-8444-444444444444"
			second.Engine = "another-alias-for-the-same-engine"
			switch variant {
			case "same-container":
				second.ComposeProject = "another-name"
			case "overlapping-services":
				second.Instances = nil
			case "reference-alias":
				second.Control = "reference"
				second.ControlGrant = ""
				second.ComposeProject = ""
				second.Services = nil
			}
			if _, err = store.SaveResource(ctx, second, ""); err == nil || err.Error() != "RESOURCE_PHYSICAL_CONFLICT" {
				t.Fatal("split the consumers of one physical service", err)
			}
			all, err := store.ListResources(ctx)
			if err != nil || len(all) != 1 {
				t.Fatal("partially published conflicting alias", err)
			}
		})
	}
}

func TestLateInstanceDiscoveryCannotCreateAnAlias(t *testing.T) {
	ctx := context.Background()
	repo, err := NewFactory().Initialize(ctx, t.TempDir(), "late alias")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	store := repo.(ports.ResourceRepository)
	first, err := store.SaveResource(
		ctx,
		resource.Record{
			SchemaVersion: "1.0",
			ResourceID:    "11111111-1111-4111-8111-111111111111",
			Control:       "managed",
			ControlGrant:  "22222222-2222-4222-8222-222222222222",
			Engine:        "desktop",
			EngineID:      "engine",
		},
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	second := resource.Record{
		SchemaVersion: "1.0",
		ResourceID:    "33333333-3333-4333-8333-333333333333",
		Control:       "reference",
		Engine:        "desktop",
		EngineID:      "engine",
		Instances:     []run.Identity{{Kind: "container", ContainerID: "discovered-later"}},
	}
	if _, err = store.SaveResource(ctx, second, ""); err != nil {
		t.Fatal(err)
	}
	spec := ports.ComposeSpec{
		ResourceID:       first.ResourceID,
		Engine:           first.Engine,
		ExpectedEngineID: first.EngineID,
		ControlGrant:     first.ControlGrant,
	}
	authority := repo.(ports.ComposeAuthorization)
	if err = authority.BeginCompose(ctx, spec, "attempt"); err != nil {
		t.Fatal(err)
	}
	err = authority.RecordComposeInstances(
		ctx,
		spec,
		"attempt",
		[]run.Identity{
			{
				Kind:        "container",
				Engine:      "desktop",
				EngineID:    "engine",
				ContainerID: "discovered-later",
				ControlID:   first.ControlGrant,
			},
		},
		true,
	)
	if err == nil || err.Error() != "RESOURCE_PHYSICAL_CONFLICT" {
		t.Fatal("late discovery split physical ownership", err)
	}
}
