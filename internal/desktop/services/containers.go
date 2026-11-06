package services

import (
	"context"
	"sort"

	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type ContainerView struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Image          string   `json:"image"`
	Status         string   `json:"status"`
	ComposeProject string   `json:"composeProject"`
	ComposeService string   `json:"composeService"`
	Control        string   `json:"control"`
	Shared         bool     `json:"shared"`
	Relationship   string   `json:"relationship"`
	ProjectIDs     []string `json:"projectIds"`
}

// Containers joins observed identities with workspace records. Compose labels
// are useful display metadata and do not establish project ownership.
func (m *Manager) Containers(ctx context.Context) ([]ContainerView, error) {
	session, err := m.current()
	if err != nil {
		return nil, err
	}
	observed, err := session.Containers.Inventory(ctx, "desktop")
	if err != nil {
		return nil, err
	}
	var resources []resource.Record
	if workspace := m.Workspace(); workspace != nil && workspace.State == "ready" {
		resources, err = session.Resources.ListResources(ctx)
		if err != nil {
			return nil, err
		}
	}
	return containerViews(observed, resources), nil
}

func containerViews(observed []ports.ContainerObservation, resources []resource.Record) []ContainerView {
	byIdentity := make(map[run.Identity][]*resource.Record)
	for i := range resources {
		binding := &resources[i]
		for _, instance := range binding.Instances {
			byIdentity[instance] = append(byIdentity[instance], binding)
		}
	}
	result := make([]ContainerView, 0, len(observed))
	for _, item := range observed {
		view := ContainerView{
			ID: item.Identity.ContainerID, Name: item.Name, Image: item.Image, Status: item.Status,
			ComposeProject: item.ComposeProject, ComposeService: item.ComposeService,
			Control: "untracked", ProjectIDs: []string{},
		}
		seen := map[string]bool{}
		for _, binding := range byIdentity[item.Identity] {
			// Prefer explicit management authority over a separate reference.
			switch {
			case binding.Control == "managed" && binding.ControlGrant != "":
				view.Control = "managed"
			case view.Control == "untracked":
				view.Control = "reference"
			}
			view.Shared = view.Shared || binding.Shared
			for _, use := range binding.Users {
				if !seen[use.ProjectID] {
					view.ProjectIDs = append(view.ProjectIDs, use.ProjectID)
					seen[use.ProjectID] = true
				}
			}
		}
		view.Relationship = view.Control
		if view.Shared {
			view.Relationship = "shared"
		}
		sort.Strings(view.ProjectIDs)
		result = append(result, view)
	}
	return result
}
