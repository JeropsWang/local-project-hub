package environment

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// ReadinessAdapter polls only explicit probes. Unsupported project-specific
// adapters need a reviewed implementation; they are never treated as passing.
type ReadinessAdapter struct {
	processes  ports.ProcessAdapter
	containers ports.ContainerAdapter
	resources  ports.ResourceRepository
}

func NewReadinessAdapter(
	processes ports.ProcessAdapter,
	containers ports.ContainerAdapter,
	resources ports.ResourceRepository,
) *ReadinessAdapter {
	return &ReadinessAdapter{processes: processes, containers: containers, resources: resources}
}

func (a *ReadinessAdapter) ValidateReadiness(unit run.UnitPlan) error {
	probe := unit.Readiness
	if probe == nil {
		return nil
	}
	if probe.TimeoutSeconds < 1 || probe.TimeoutSeconds > 300 {
		return problem.New("READINESS_TIMEOUT_INVALID", unit.ID)
	}
	switch probe.Kind {
	case "process":
		if unit.Kind == "process" && a.processes != nil {
			return nil
		}
	case "tcp":
		if probe.Port > 0 && probe.Port <= 65535 && probe.Host != "" {
			return nil
		}
	case "adapter":
		if probe.AdapterID == "managed-control-v1" && unit.Kind == "process" {
			if _, ok := a.processes.(ports.ProcessStatus); ok {
				return nil
			}
		}
		if probe.AdapterID == "docker-health" && a.containers != nil && a.resources != nil &&
			(unit.Kind == "compose" || unit.Kind == "external") {
			return nil
		}
	}
	return problem.New("READINESS_ADAPTER_UNSUPPORTED", unit.ID)
}

func (a *ReadinessAdapter) WaitReady(ctx context.Context, unit run.UnitPlan, instance run.Identity) error {
	if err := a.ValidateReadiness(unit); err != nil {
		return err
	}
	if unit.Readiness == nil {
		return nil
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(unit.Readiness.TimeoutSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if unit.Kind == "process" {
			state, err := a.processes.Inspect(bounded, instance)
			if err != nil {
				return err
			}
			if state.ActiveProcesses == 0 || state.ExitFailure || (!state.RootRunning && state.ExitCode != 0) {
				return problem.New("PROCESS_EXITED_BEFORE_READY", unit.ID)
			}
		}
		ready := false
		switch unit.Readiness.Kind {
		case "process":
			ready = true
		case "tcp":
			connection, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(
				bounded,
				"tcp",
				net.JoinHostPort(unit.Readiness.Host, strconv.Itoa(unit.Readiness.Port)),
			)
			if err == nil {
				connection.Close()
				ready = true
			}
		case "adapter":
			if unit.Readiness.AdapterID == "managed-control-v1" {
				state, err := a.processes.(ports.ProcessStatus).ControlStatus(bounded, instance)
				ready = err == nil && state == "running"
			} else {
				var err error
				ready, err = a.dockerReady(bounded, unit.ResourceID)
				if err != nil {
					return err
				}
			}
		}
		if ready {
			return nil
		}
		select {
		case <-bounded.Done():
			if err := ctx.Err(); err != nil {
				return err
			}
			return problem.New("READINESS_TIMEOUT", unit.ID)
		case <-ticker.C:
		}
	}
}

func (a *ReadinessAdapter) dockerReady(ctx context.Context, id string) (bool, error) {
	record, err := a.resources.Resource(ctx, id)
	if err != nil {
		return false, err
	}
	observed, err := a.containers.Inventory(ctx, record.Engine)
	if err != nil {
		return false, err
	}
	if len(record.Instances) == 0 || record.PendingStart != "" {
		return false, nil
	}
	for _, expected := range record.Instances {
		found := false
		for _, item := range observed {
			actual := item.Identity
			actual.ControlID = expected.ControlID
			if actual == expected {
				found = item.Running && item.Health == "healthy"
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}
