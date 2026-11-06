package services

import (
	"context"
	"sort"
)

type ContainerView struct {
	Name           string   `json:"name"`
	Image          string   `json:"image"`
	Status         string   `json:"status"`
	ComposeProject string   `json:"composeProject"`
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
	resources, err := session.Resources.ListResources(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]ContainerView, 0, len(observed))
	for _, item := range observed {
		view := ContainerView{Name: item.Name, Image: item.Image, Status: item.Status, ComposeProject: item.ComposeProject, Relationship: "untracked"}
		seen := map[string]bool{}
		for _, resource := range resources {
			matched := false
			for _, instance := range resource.Instances {
				if instance == item.Identity {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			view.Relationship = resource.Control
			if resource.Shared {
				view.Relationship = "shared"
			}
			for _, use := range resource.Users {
				if !seen[use.ProjectID] {
					view.ProjectIDs = append(view.ProjectIDs, use.ProjectID)
					seen[use.ProjectID] = true
				}
			}
		}
		sort.Strings(view.ProjectIDs)
		result = append(result, view)
	}
	return result, nil
}
