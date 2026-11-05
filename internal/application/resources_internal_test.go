package application

import (
	"context"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/adapters/persistence"
	"github.com/JeropsWang/local-project-hub/internal/domain/resource"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type pendingContainers struct{ stops int }

func (p *pendingContainers) Inventory(context.Context, string) ([]ports.ContainerObservation, error) {
	return nil, nil
}
func (p *pendingContainers) Start(context.Context, ports.ComposeSpec) ([]run.Identity, error) {
	return nil, nil
}
func (p *pendingContainers) Stop(context.Context, run.Identity) (ports.ContainerStopResult, error) {
	p.stops++
	return ports.ContainerStopResult{Graceful: true}, nil
}

func TestUnresolvedComposeIntentCannotBeReportedFullyStopped(t *testing.T) {
	ctx := context.Background()
	ws := NewWorkspaceService(persistence.NewFactory())
	if _, err := ws.Initialize(ctx, InitWorkspaceRequest{Root: t.TempDir(), Name: "pending start"}); err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	record := resource.Record{
		SchemaVersion: "1.0",
		ResourceID:    "22222222-2222-4222-8222-222222222222",
		Control:       "managed",
		Engine:        "desktop",
		EngineID:      "fixture-engine",
		ControlGrant:  "33333333-3333-4333-8333-333333333333",
		PendingStart:  "44444444-4444-4444-8444-444444444444",
	}
	if _, err := ws.current.(ports.ResourceRepository).SaveResource(ctx, record, ""); err != nil {
		t.Fatal(err)
	}
	containers := &pendingContainers{}
	result, err := NewResourceService(ws, containers, nil).StopUnused(ctx, record.ResourceID, true)
	if err != nil || result.Stopped || result.Reason != "start-unresolved" || containers.stops != 0 {
		t.Fatalf("uncertain startup disappeared: %+v %v", result, err)
	}
	// This fixture has no external processes: resolve its synthetic intent so
	// TempDir cleanup can release the otherwise deliberately protected workspace.
	spec := ports.ComposeSpec{
		ResourceID:       record.ResourceID,
		Engine:           record.Engine,
		ExpectedEngineID: record.EngineID,
		ControlGrant:     record.ControlGrant,
	}
	if err := ws.current.(ports.ComposeAuthorization).RecordComposeInstances(ctx, spec, record.PendingStart, nil, true); err != nil {
		t.Fatal(err)
	}
}
