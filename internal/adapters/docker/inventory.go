package docker

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// Inventory reads engine/instance/Compose evidence without adding a local control
// grant. Source checkout labels are shown as evidence, not treated as authority.
func (a *NativeContainerAdapter) Inventory(ctx context.Context, engine string) ([]ports.ContainerObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	engineID, err := a.engineInfo(ctx, engine)
	if err != nil {
		return nil, err
	}
	var listed []struct {
		ID    string `json:"Id"`
		Image string
	}
	if err := a.request(ctx, engine, http.MethodGet, "/containers/json?all=1", &listed); err != nil {
		return nil, err
	}
	if len(listed) > 10000 {
		return nil, problem.New("CONTAINER_INVENTORY_LIMIT", "")
	}
	result := make([]ports.ContainerObservation, len(listed))
	jobs := make(chan int, len(listed))
	for i := range listed {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	var firstError error
	var errorMu sync.Mutex
	for range min(4, len(listed)) {
		workers.Go(func() {
			for i := range jobs {
				item, err := a.inspect(ctx, engine, listed[i].ID)
				if err != nil {
					errorMu.Lock()
					if firstError == nil {
						firstError = err
					}
					errorMu.Unlock()
					continue
				}
				observation := ports.ContainerObservation{
					Identity: run.Identity{
						Kind:         "container",
						Engine:       engine,
						EngineID:     engineID,
						ContainerID:  item.ID,
						CreationTime: item.Created,
						StartedAt:    item.State.StartedAt,
					},
					Name:           strings.TrimPrefix(item.Name, "/"),
					Image:          listed[i].Image,
					Status:         item.State.Status,
					Running:        item.State.Running,
					Health:         "unknown",
					ComposeProject: item.Config.Labels["com.docker.compose.project"],
					ComposeService: item.Config.Labels["com.docker.compose.service"],
					ComposeFiles:   item.Config.Labels["com.docker.compose.project.config_files"],
					WorkingDir:     item.Config.Labels["com.docker.compose.project.working_dir"],
					ConfigHash:     item.Config.Labels["com.docker.compose.config-hash"],
				}
				if item.State.Health != nil {
					observation.Health = item.State.Health.Status
				}
				observation.ManagerResourceID = item.Config.Labels[managerResourceLabel]
				observation.ManagerGrantID = item.Config.Labels[managerGrantLabel]
				observation.ManagerOperationID = item.Config.Labels[managerOperationLabel]
				result[i] = observation
			}
		})
	}
	workers.Wait()
	if firstError != nil {
		return nil, firstError
	}
	return result, nil
}
