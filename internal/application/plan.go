package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/task"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// PlanService stores backend-authored steps bound to a fixed input fingerprint.
type PlanService struct {
	workspaces      *WorkspaceService
	checks          *CheckService
	runtimeProvider ports.RuntimeProvider
	projectPreparer ports.ProjectPreparer
}

// PlanOption supplies backend capabilities; no option can be read from project JSON.
type PlanOption func(*PlanService)

// WithRuntimeProvider pins concrete supplies while preparing a preview.
func WithRuntimeProvider(provider ports.RuntimeProvider) PlanOption {
	return func(s *PlanService) { s.runtimeProvider = provider }
}

// WithProjectPreparer includes clean source copies and dependency effects in the preview.
func WithProjectPreparer(preparer ports.ProjectPreparer) PlanOption {
	return func(s *PlanService) { s.projectPreparer = preparer }
}

// NewPlanService does not execute any step while constructing a plan.
func NewPlanService(ws *WorkspaceService, checks *CheckService, options ...PlanOption) *PlanService {
	s := &PlanService{workspaces: ws, checks: checks}
	for _, option := range options {
		option(s)
	}
	return s
}

// BuildPrepare permits missing environments and describes explicit preparation effects.
func (s *PlanService) BuildPrepare(ctx context.Context, ref ProfileRef) (OperationPlan, error) {
	return s.build(ctx, task.Prepare, ref)
}

// BuildStart requires the full selected profile's checks and graceful stop declarations.
func (s *PlanService) BuildStart(ctx context.Context, ref ProfileRef) (OperationPlan, error) {
	return s.build(ctx, task.Start, ref)
}

// BuildStop builds a graceful stop plan. Run snapshots replace editor data at execution.
func (s *PlanService) BuildStop(ctx context.Context, ref ProfileRef) (OperationPlan, error) {
	return s.build(ctx, task.Stop, ref)
}

func (s *PlanService) build(ctx context.Context, kind task.Kind, ref ProfileRef) (OperationPlan, error) {
	input, doc, report, err := s.input(ctx, ref)
	if err != nil {
		return OperationPlan{}, err
	}
	if kind == task.Start && report.Summary != "ready" {
		return OperationPlan{}, problem.New("CHECKS_NOT_READY", "")
	}
	ordered, err := project.Order(doc, string(ref.ProfileID))
	if err != nil {
		return OperationPlan{}, err
	}
	id, err := identity.NewUUID()
	if err != nil {
		return OperationPlan{}, err
	}
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo := s.workspaces.current
	if repo == nil {
		return OperationPlan{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	if repo.Info().WorkspaceID != input.WorkspaceID {
		return OperationPlan{}, problem.New("PLAN_STALE", "")
	}
	fresh, err := repo.Get(ctx, string(ref.ProjectID))
	if err != nil || fresh.Revision != ref.ExpectedRevision {
		return OperationPlan{}, problem.New("PLAN_STALE", "")
	}
	store, ok := repo.(ports.TaskRepository)
	if !ok {
		return OperationPlan{}, problem.New("TASK_STORAGE_UNAVAILABLE", "")
	}
	plan := OperationPlan{
		ID:               task.PlanID(id),
		Kind:             kind,
		WorkspaceID:      input.WorkspaceID,
		ProfileRef:       &ref,
		Fingerprint:      input.Hash(),
		Steps:            []task.Step{},
		Warnings:         []string{},
		RequiredBindings: []string{},
		ResourceIDs:      []string{},
		CreatedAt:        time.Now().UTC(),
	}
	plan.SourceConstraints = report.Constraints
	resources := map[string]bool{}
	requirements := map[string]bool{}
	for _, unitID := range ordered {
		unit := doc.Units[unitID]
		resources[unit.ResourceID] = true
		for _, key := range unit.RequirementIDs {
			requirements[key] = true
		}
		if kind == task.Start && unit.Control == "managed" && (unit.Kind == "process" || unit.Kind == "compose") &&
			(unit.Stop == nil || unit.Stop.Kind == "manual" || unit.Stop.Kind == "none") {
			return OperationPlan{}, problem.New("AUTOMATIC_STOP_UNSUPPORTED", unitID)
		}
	}
	for id := range resources {
		plan.ResourceIDs = append(plan.ResourceIDs, id)
	}
	sort.Strings(plan.ResourceIDs)
	if kind == task.Prepare {
		runtimePlans := map[string]ports.RuntimeInstallPlan{}
		keys := []string{}
		for key := range requirements {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			requirement := doc.Requirements[key]
			if !requirement.Required || requirement.Location == "docker" {
				continue
			}
			fingerprints := map[string]string{"plan": plan.Fingerprint}
			if s.runtimeProvider != nil {
				runtimePlan, err := s.runtimeProvider.Plan(
					ctx,
					ports.RuntimeRequest{Requirement: requirement, Constraints: report.Constraints},
				)
				if err != nil {
					return OperationPlan{}, err
				}
				fingerprints["runtime"] = runtimePlan.Fingerprint
				fingerprints["artifactId"] = runtimePlan.ArtifactID
				fingerprints["version"] = runtimePlan.Version
				runtimePlans[key] = runtimePlan
			}
			plan.Steps = append(
				plan.Steps,
				task.Step{
					ID:                "runtime-" + key,
					Action:            "prepare-runtime",
					Effect:            "install",
					CancelPolicy:      "checkpoint",
					RequirementID:     key,
					ResourceIDs:       []string{},
					InputFingerprints: fingerprints,
				},
			)
		}
		if s.projectPreparer != nil {
			prepared, err := s.projectPreparer.Plan(
				ctx,
				ports.ProjectPreparationRequest{
					Ref:          ref,
					Document:     doc,
					UnitIDs:      ordered,
					Bindings:     input.Bindings,
					RuntimePlans: runtimePlans,
				},
			)
			if err != nil {
				return OperationPlan{}, err
			}
			plan.ProjectPreparation = &prepared
			plan.Warnings = append(plan.Warnings, prepared.Warnings...)
			plan.Steps = append(
				plan.Steps,
				task.Step{
					ID:                "project-environment",
					Action:            "prepare-project",
					Effect:            "source-copy-and-package-install-scripts",
					CancelPolicy:      "checkpoint",
					ResourceIDs:       []string{},
					InputFingerprints: map[string]string{"source": prepared.Fingerprint},
				},
			)
		}
	} else {
		if kind == task.Stop {
			for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
				ordered[i], ordered[j] = ordered[j], ordered[i]
			}
		}
		for _, unitID := range ordered {
			unit := doc.Units[unitID]
			if unit.Control != "managed" {
				continue
			}
			plan.Steps = append(plan.Steps, task.Step{ID: string(kind) + "-" + unitID, Action: string(kind) + "-" + unit.Kind, Effect: string(kind), CancelPolicy: "wait", UnitID: unitID, ResourceIDs: []string{unit.ResourceID}, InputFingerprints: map[string]string{"plan": plan.Fingerprint}})
		}
	}
	for _, item := range report.Items {
		for _, reason := range item.Reasons {
			plan.Warnings = append(plan.Warnings, reason.Code)
		}
		if len(item.RequirementID) > 8 && item.RequirementID[:8] == "binding:" && item.Status != "satisfied" {
			plan.RequiredBindings = append(plan.RequiredBindings, item.RequirementID[8:])
		}
	}
	if err := store.SavePlan(ctx, plan); err != nil {
		return OperationPlan{}, err
	}
	return plan, nil
}

type planInput struct {
	WorkspaceID       string
	Root              string
	WorkspaceState    string
	Revision          int32
	DocumentHash      string
	SourceFingerprint string
	Bindings          map[string]project.Binding
	Checks            map[string]string
}

func (i planInput) Hash() string {
	data, _ := json.Marshal(i)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *PlanService) input(ctx context.Context, ref ProfileRef) (planInput, project.Document, CheckReport, error) {
	report, err := s.checks.Check(ctx, CheckRequest{ProfileRef: ref})
	if err != nil {
		return planInput{}, project.Document{}, report, err
	}
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo := s.workspaces.current
	if repo == nil {
		return planInput{}, project.Document{}, report, problem.New("WORKSPACE_REQUIRED", "")
	}
	view, err := repo.Get(ctx, string(ref.ProjectID))
	if err != nil {
		return planInput{}, project.Document{}, report, err
	}
	if view.Revision != ref.ExpectedRevision {
		return planInput{}, project.Document{}, report, problem.New("REVISION_CONFLICT", "")
	}
	input := planInput{
		WorkspaceID:       repo.Info().WorkspaceID,
		Root:              repo.Info().Root,
		WorkspaceState:    repo.Info().State,
		Revision:          view.Revision,
		DocumentHash:      view.ContentHash,
		SourceFingerprint: report.SourceFingerprint,
		Bindings:          map[string]project.Binding{},
		Checks:            map[string]string{},
	}
	keys := map[string]bool{}
	for _, unit := range view.Document.Units {
		if unit.Binding != "" {
			keys[unit.Binding] = true
		}
		if unit.EngineBinding != "" {
			keys[unit.EngineBinding] = true
		}
		for _, command := range unitCommands(unit) {
			if command == nil {
				continue
			}
			keys[command.CWD.Binding] = true
			if command.Executable.Binding != "" {
				keys[command.Executable.Binding] = true
			}
			for _, key := range command.EnvBindings {
				keys[key] = true
			}
		}
	}
	for key := range view.Document.Requirements {
		keys[key] = true
	}
	for _, file := range view.Document.Files {
		if file.Path != nil {
			keys[file.Path.Binding] = true
		}
	}
	for key := range keys {
		binding, err := repo.Binding(ctx, string(ref.ProjectID), key)
		if err == nil {
			input.Bindings[key] = binding
		} else if err.Error() != "BINDING_MISSING" {
			return planInput{}, project.Document{}, report, err
		}
	}
	for _, item := range report.Items {
		value := string(item.Status)
		for _, evidence := range item.Evidence {
			value += "|" + evidence.Kind + ":" + evidence.Source + ":" + evidence.Version
		}
		input.Checks[item.RequirementID] = value
	}
	return input, view.Document, report, nil
}

// validate compares fresh machine/configuration inputs; plan timestamps are deliberately excluded.
func (s *PlanService) validate(ctx context.Context, plan task.Plan) error {
	if plan.ProfileRef == nil {
		return problem.New("PLAN_REFERENCE_REQUIRED", "")
	}
	input, doc, report, err := s.input(ctx, *plan.ProfileRef)
	if err != nil || input.WorkspaceID != plan.WorkspaceID || input.Hash() != plan.Fingerprint {
		return problem.New("PLAN_STALE", "")
	}
	runtimePlans := map[string]ports.RuntimeInstallPlan{}
	for _, step := range plan.Steps {
		if step.Action != "prepare-runtime" || step.InputFingerprints["runtime"] == "" {
			continue
		}
		if s.runtimeProvider == nil {
			return problem.New("PLAN_STALE", "")
		}
		runtimePlan, err := s.runtimeProvider.Plan(
			ctx,
			ports.RuntimeRequest{Requirement: doc.Requirements[step.RequirementID], Constraints: report.Constraints},
		)
		if err != nil || runtimePlan.Fingerprint != step.InputFingerprints["runtime"] {
			return problem.New("PLAN_STALE", "")
		}
		runtimePlans[step.RequirementID] = runtimePlan
	}
	if plan.ProjectPreparation != nil {
		if s.projectPreparer == nil {
			return problem.New("PLAN_STALE", "")
		}
		ordered, err := project.Order(doc, string(plan.ProfileRef.ProfileID))
		if err != nil {
			return problem.New("PLAN_STALE", "")
		}
		fresh, err := s.projectPreparer.Plan(
			ctx,
			ports.ProjectPreparationRequest{
				Ref:          *plan.ProfileRef,
				Document:     doc,
				UnitIDs:      ordered,
				Bindings:     input.Bindings,
				RuntimePlans: runtimePlans,
			},
		)
		if err != nil || fresh.Fingerprint != plan.ProjectPreparation.Fingerprint {
			return problem.New("PLAN_STALE", "")
		}
	}
	return nil
}

// requestHash binds the idempotence key to the exact submitted plan and fingerprint.
func requestHash(req SubmitPlanRequest) string {
	data, _ := json.Marshal(req)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func taskStorage(repo ports.Repository) (ports.TaskRepository, error) {
	store, ok := repo.(ports.TaskRepository)
	if !ok {
		return nil, problem.New("TASK_STORAGE_UNAVAILABLE", "")
	}
	return store, nil
}

func safeTaskError(err error) *task.AppError {
	var known *problem.Error
	if errors.As(err, &known) {
		return &task.AppError{Code: known.Code, Message: "The operation could not complete.", Retryable: true}
	}
	return &task.AppError{
		Code:      "TASK_EXECUTION_FAILED",
		Message:   "The operation failed; inspect the controlled task log.",
		Retryable: true,
	}
}

func parseCursor(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < 0 || fmt.Sprint(number) != value {
		return 0, problem.New("TASK_CURSOR_INVALID", "")
	}
	return number, nil
}
