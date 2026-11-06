package services

import (
	"context"

	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
)

// PlanPreview exposes reviewable effects without private backend snapshots.
type PlanPreview struct {
	ID               task.PlanID `json:"id"`
	Fingerprint      string      `json:"fingerprint"`
	Steps            []task.Step `json:"steps"`
	Warnings         []string    `json:"warnings"`
	RequiredBindings []string    `json:"requiredBindings"`
}

func preview(plan task.Plan) PlanPreview {
	return PlanPreview{ID: plan.ID, Fingerprint: plan.Fingerprint, Steps: plan.Steps, Warnings: plan.Warnings, RequiredBindings: plan.RequiredBindings}
}

func (m *Manager) Plan(ctx context.Context, kind task.Kind, ref application.ProfileRef) (PlanPreview, error) {
	session, err := m.current()
	if err != nil {
		return PlanPreview{}, err
	}
	var plan task.Plan
	switch kind {
	case task.Prepare:
		plan, err = session.Plans.BuildPrepare(ctx, ref)
	case task.Start:
		plan, err = session.Plans.BuildStart(ctx, ref)
	case task.Stop:
		plan, err = session.Plans.BuildStop(ctx, ref)
	default:
		return PlanPreview{}, problem.New("PLAN_KIND_UNSUPPORTED", "")
	}
	return preview(plan), err
}

func (m *Manager) Submit(ctx context.Context, req task.SubmitRequest) (task.View, error) {
	session, err := m.current()
	if err != nil {
		return task.View{}, err
	}
	return session.Tasks.Submit(ctx, req)
}

func (m *Manager) Task(ctx context.Context, id task.ID) (task.View, error) {
	session, err := m.current()
	if err != nil {
		return task.View{}, err
	}
	return session.Tasks.Get(ctx, id)
}

func (m *Manager) Resolve(ctx context.Context, req task.ResolveRequest) (task.View, error) {
	session, err := m.current()
	if err != nil {
		return task.View{}, err
	}
	return session.Tasks.Resolve(ctx, req)
}

func (m *Manager) RequestExit(ctx context.Context) (task.View, error) {
	session, err := m.current()
	if err != nil {
		return task.View{}, err
	}
	return session.Lifecycle.RequestExit(ctx)
}

func (m *Manager) CanQuit(ctx context.Context) (bool, error) {
	session, err := m.current()
	if err != nil {
		return true, nil
	}
	return session.Lifecycle.CanQuit(ctx)
}
