package process

import (
	"context"
	"net"

	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

// ControlStatus authenticates the original Job's server without issuing a save
// or shutdown operation. Tokens are kept inside the live process adapter.
func (a *NativeProcessAdapter) ControlStatus(ctx context.Context, id run.Identity) (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	instance, err := a.original(id)
	if err != nil {
		return "", err
	}
	operation, err := identity.NewUUID()
	if err != nil {
		return "", err
	}
	client := NewControlClient()
	client.dial = func(ctx context.Context, _ run.Identity) (net.Conn, error) {
		return dialOwnedControlPipe(ctx, instance)
	}
	result, err := client.Exchange(ctx, id, instance.token, operation, "status")
	if err != nil {
		return "", err
	}
	if result.ErrorCode != "" {
		return "", problem.New(result.ErrorCode, "")
	}
	return result.State, nil
}
