package services

import (
	"slices"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func TestContainerViewSeparatesSharedUseFromControlAuthority(t *testing.T) {
	identity := run.Identity{Kind: "container", EngineID: "local", ContainerID: "123", StartedAt: "current"}
	stale := identity
	stale.StartedAt = "old"
	views := containerViews(
		[]ports.ContainerObservation{{Identity: identity, ComposeService: "web"}},
		[]resource.Record{
			{Instances: []run.Identity{stale}, Control: "managed", ControlGrant: "old-grant", Users: []resource.Use{{ProjectID: "old"}}},
			{Instances: []run.Identity{identity}, Control: "reference", Shared: true, Users: []resource.Use{{ProjectID: "a"}, {ProjectID: "a"}, {ProjectID: "b"}}},
		},
	)
	if len(views) != 1 {
		t.Fatalf("unexpected views: %+v", views)
	}
	view := views[0]
	if view.ID != "123" || view.ComposeService != "web" || view.Control != "reference" || !view.Shared ||
		view.Relationship != "shared" || !slices.Equal(view.ProjectIDs, []string{"a", "b"}) {
		t.Fatalf("unexpected relationship: %+v", view)
	}
}
