package services

import (
	"context"

	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

// Runs loads recent status summaries for the selected project.
func (m *Manager) Runs(ctx context.Context, id application.ProjectID) ([]application.RunView, error) {
	session, err := m.current()
	if err != nil {
		return nil, err
	}
	return session.RunHistory.Runs(ctx, id)
}

// ReadLog reads an incremental page for a unit in one journaled run.
func (m *Manager) ReadLog(ctx context.Context, id application.ProjectID, runID application.RunID, unitID string, after application.EventCursor, limit int) (run.LogPage, error) {
	session, err := m.current()
	if err != nil {
		return run.LogPage{}, err
	}
	return session.RunHistory.ReadLog(ctx, id, runID, unitID, after, limit)
}
