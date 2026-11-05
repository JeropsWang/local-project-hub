package process

import (
	"context"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

// ReadLog checks original ownership and holds the writer lock during pagination.
func (a *NativeProcessAdapter) ReadLog(
	ctx context.Context,
	identity run.Identity,
	after string,
	limit int,
) (run.LogPage, error) {
	if err := ctx.Err(); err != nil {
		return run.LogPage{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	instance, err := a.original(identity)
	if err != nil {
		return run.LogPage{}, err
	}
	return instance.log.Read(after, limit)
}
