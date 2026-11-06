package environment

import (
	"context"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type deadlineContainers struct{ ports.ContainerAdapter }

func (deadlineContainers) Inventory(ctx context.Context, _ string) ([]ports.ContainerObservation, error) {
	<-ctx.Done()
	return nil, problem.New("DOCKER_CONNECTION_FAILED", "")
}

type readinessResources struct{ ports.ResourceRepository }

func (readinessResources) Resource(context.Context, string) (resource.Record, error) {
	return resource.Record{Engine: "desktop"}, nil
}

func TestDockerReadinessReportsDeadlineRatherThanConnectionFailure(t *testing.T) {
	adapter := NewReadinessAdapter(nil, deadlineContainers{}, readinessResources{})
	unit := run.UnitPlan{
		ID: "database", Kind: "external",
		Readiness: &project.Probe{Kind: "adapter", AdapterID: "docker-health", TimeoutSeconds: 1},
	}
	err := adapter.WaitReady(context.Background(), unit, run.Identity{})
	if err == nil || err.Error() != "READINESS_TIMEOUT" {
		t.Fatalf("got %v, want READINESS_TIMEOUT", err)
	}
}
