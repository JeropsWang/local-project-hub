package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// CheckRequest confirms the saved feature selection; edits must be saved before checking.
type CheckRequest struct {
	ProfileRef
	EnabledFeatureIDs []string `json:"enabledFeatureIds,omitempty"`
}

// CheckReport retains profile identity alongside independent metadata evidence.
type CheckReport = check.Report

// CheckService shares a four-probe budget across simultaneous callers.
type CheckService struct {
	workspaces *WorkspaceService
	probe      ports.Probe
	files      ports.FileInspector
	evaluate   ports.VersionEvaluator
	semaphore  chan struct{}
}

// NewCheckService wires read-only ports. A missing evaluator never implies success.
func NewCheckService(ws *WorkspaceService, probe ports.Probe, files ports.FileInspector, evaluate ports.VersionEvaluator) *CheckService {
	return &CheckService{workspaces: ws, probe: probe, files: files, evaluate: evaluate, semaphore: make(chan struct{}, 4)}
}

// Check inspects only the fixed revision and never installs or starts project code.
func (s *CheckService) Check(ctx context.Context, req CheckRequest) (CheckReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s.workspaces.mu.RLock()
	defer s.workspaces.mu.RUnlock()
	repo := s.workspaces.current
	if repo == nil {
		return CheckReport{}, problem.New("WORKSPACE_REQUIRED", "")
	}
	view, err := repo.Get(ctx, string(req.ProjectID))
	if err != nil {
		return CheckReport{}, err
	}
	if req.ExpectedRevision != view.Revision {
		return CheckReport{}, problem.New("REVISION_CONFLICT", "")
	}
	profile, ok := view.Document.Profiles[string(req.ProfileID)]
	if !ok {
		return CheckReport{}, problem.New("PROFILE_MISSING", "")
	}
	if req.EnabledFeatureIDs != nil && !sameFeatures(req.EnabledFeatureIDs, profile.FeatureIDs) {
		return CheckReport{}, problem.New("PROFILE_FEATURES_UNSAVED", "")
	}
	units, err := project.Order(view.Document, string(req.ProfileID))
	if err != nil {
		return CheckReport{}, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return CheckReport{}, err
	}
	report := CheckReport{ID: hex.EncodeToString(nonce[:]), ProjectID: string(req.ProjectID), ProfileID: string(req.ProfileID), ExpectedRevision: view.Revision, CreatedAt: time.Now().UTC(), Items: []check.Item{}}
	requirements := map[string]bool{}
	bindingKeys := map[string]bool{}
	for _, id := range units {
		unit := view.Document.Units[id]
		for _, key := range unit.RequirementIDs {
			requirements[key] = view.Document.Requirements[key].Required || requirements[key]
		}
		for _, command := range unitCommands(unit) {
			if command == nil {
				continue
			}
			if command.Executable.Kind == "runtime" {
				requirements[command.Executable.RequirementID] = true
			}
			if command.Executable.Kind == "binding" {
				bindingKeys[command.Executable.Binding] = true
			}
			bindingKeys[command.CWD.Binding] = true
			for _, key := range command.EnvBindings {
				bindingKeys[key] = true
			}
		}
		if unit.Binding != "" {
			bindingKeys[unit.Binding] = true
		}
		if unit.EngineBinding != "" {
			bindingKeys[unit.EngineBinding] = true
		}
	}
	for _, file := range view.Document.Files {
		if file.Path != nil && file.Required {
			bindingKeys[file.Path.Binding] = true
		}
	}
	bindings := map[string]project.Binding{}
	for key := range bindingKeys {
		binding, err := repo.Binding(ctx, string(req.ProjectID), key)
		item := check.Item{RequirementID: "binding:" + key, Status: check.Satisfied, Evidence: []check.Evidence{}, Reasons: []check.Reason{}, Repairs: []check.RepairStep{}}
		if err != nil {
			item.Status = check.BindingRequired
			item.Reasons = append(item.Reasons, check.Reason{Code: "BINDING_MISSING", Subject: key, Message: "Bind the required target resource."})
		} else {
			bindings[key] = binding
		}
		report.Items = append(report.Items, item)
	}
	if repo.Info().State != "ready" {
		report.Items = append(report.Items, check.Item{RequirementID: "workspace", Status: check.BindingRequired, Reasons: []check.Reason{{Code: "WORKSPACE_REBIND_REQUIRED", Message: "Confirm relocation and bind target resources again."}}})
	}
	ids := make([]string, 0, len(requirements))
	inspection, inspectionErr := s.files.Inspect(ctx, ports.InspectionRequest{Document: view.Document, Bindings: bindings, WorkspaceRoot: repo.Info().Root, Revision: view.Revision})
	if inspectionErr != nil {
		report.Items = append(report.Items, check.Item{RequirementID: "source", Status: check.Unknown, Reasons: []check.Reason{{Code: "SOURCE_INSPECTION_FAILED"}}})
	} else {
		report.SourceFingerprint = inspection.Fingerprint
		report.Items = append(report.Items, inspection.Items...)
	}
	for id := range requirements {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	type result struct {
		id   string
		item check.Item
	}
	jobs := make(chan string, len(ids))
	results := make(chan result, len(ids))
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for id := range jobs {
				item := check.Item{RequirementID: id, Status: check.Unknown, Reasons: []check.Reason{}, Evidence: []check.Evidence{}, Repairs: []check.RepairStep{}}
				select {
				case s.semaphore <- struct{}{}:
					req := view.Document.Requirements[id]
					probeReq := ports.ProbeRequest{Tool: req.Component, Location: req.Location}
					if binding, e := repo.Binding(ctx, string(view.Document.ProjectID), id); e == nil && binding.Kind == "runtime" {
						probeReq.Executable = binding.LocalLocator
					}
					observation, e := s.probe.Discover(ctx, probeReq)
					<-s.semaphore
					if e != nil {
						item.Reasons = append(item.Reasons, check.Reason{Code: "PROBE_FAILED", Subject: id, Message: "The read-only probe did not complete."})
					} else {
						item.Evidence = append(item.Evidence, observation.Evidence)
						if s.evaluate != nil {
							evaluated, e := s.evaluate(req, observation)
							if e == nil {
								item = evaluated
								item.RequirementID = id
								for _, constraint := range inspection.Constraints {
									if constraint.Component != req.Component || req.Location != "windows" {
										continue
									}
									extra := req
									extra.Semantics = constraint.Semantics
									extra.Range = constraint.Range
									extra.Evidence = project.RequirementEvidence{Kind: "lockfile", Reference: constraint.Source}
									more, err := s.evaluate(extra, observation)
									if err != nil {
										item.Status = check.Unknown
										continue
									}
									item.Evidence = append(item.Evidence, check.Evidence{Kind: "metadata", Source: constraint.Source, ObservedAt: report.CreatedAt})
									if more.Status != check.Satisfied {
										if item.Status == check.Satisfied || more.Status == check.Incompatible {
											item.Status = more.Status
										}
										for _, reason := range more.Reasons {
											reason.Subject = constraint.Source
											item.Reasons = append(item.Reasons, reason)
										}
									}
								}
							} else {
								item.Reasons = append(item.Reasons, check.Reason{Code: "VERSION_EVALUATION_FAILED", Subject: id})
							}
						} else {
							item.Status = observation.Status
							if item.Status == "" {
								item.Status = check.Unknown
							}
							code := observation.ReasonCode
							if code == "" {
								code = "VERSION_EVALUATOR_REQUIRED"
							}
							item.Reasons = append(item.Reasons, check.Reason{Code: code, Subject: id})
						}
					}
				case <-ctx.Done():
					item.Reasons = append(item.Reasons, check.Reason{Code: "CHECK_BUDGET_EXCEEDED", Subject: id})
				}
				results <- result{id: id, item: item}
			}
		})
	}
	workers.Wait()
	close(results)
	for result := range results {
		report.Items = append(report.Items, result.item)
	}
	sort.Slice(report.Items, func(i, j int) bool { return report.Items[i].RequirementID < report.Items[j].RequirementID })
	report.Summary = "ready"
	for _, item := range report.Items {
		required, requirement := requirements[item.RequirementID]
		if requirement && !required {
			continue
		}
		if item.Status == check.Missing || item.Status == check.Incompatible || item.Status == check.BindingRequired {
			report.Summary = "not-ready"
			break
		}
		if item.Status != check.Satisfied {
			report.Summary = "unverified"
		}
	}
	return report, nil
}

func sameFeatures(first, second []string) bool {
	a := append([]string{}, first...)
	b := append([]string{}, second...)
	sort.Strings(a)
	sort.Strings(b)
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

func unitCommands(unit project.Unit) []*project.Command {
	result := []*project.Command{unit.Start, unit.Command}
	if unit.Stop != nil {
		result = append(result, unit.Stop.Command)
	}
	return result
}
