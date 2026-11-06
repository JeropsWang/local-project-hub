package application

import (
	"context"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// RunView presents status without serializing immutable executable snapshots.
type RunView struct {
	ID          string        `json:"id"`
	ProfileID   ProfileID     `json:"profileId"`
	Revision    Revision      `json:"revision"`
	State       string        `json:"state"`
	CreatedAt   time.Time     `json:"createdAt"`
	FailureCode string        `json:"failureCode,omitempty"`
	Units       []RunUnitView `json:"units"`
}

type RunUnitView struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Kind          string `json:"kind"`
	LogsAvailable bool   `json:"logsAvailable"`
}

type RunHistoryService struct {
	workspace *WorkspaceService
	logs      ports.ProcessLogs
}

func NewRunHistoryService(workspace *WorkspaceService, logs ports.ProcessLogs) *RunHistoryService {
	return &RunHistoryService{workspace: workspace, logs: logs}
}

// Runs lists recent starts for a project, including stopped runs.
func (s *RunHistoryService) Runs(ctx context.Context, projectID ProjectID) ([]RunView, error) {
	s.workspace.mu.RLock()
	defer s.workspace.mu.RUnlock()
	store, err := s.repository()
	if err != nil {
		return nil, err
	}
	records, err := store.RecentRuns(ctx, string(projectID))
	if err != nil {
		return nil, err
	}
	result := make([]RunView, 0, len(records))
	for _, record := range records {
		snapshot := record.Snapshot
		view := RunView{
			ID: snapshot.ID, ProfileID: snapshot.ProfileRef.ProfileID,
			Revision: snapshot.ProfileRef.ExpectedRevision, State: record.State,
			CreatedAt: snapshot.CreatedAt, FailureCode: record.FailureCode,
			Units: make([]RunUnitView, 0, len(snapshot.Units)),
		}
		identities := make(map[string]run.Identity, len(record.Acquisitions))
		for _, acquisition := range record.Acquisitions {
			if acquisition.Identity.Kind == "process" {
				identities[acquisition.UnitID] = acquisition.Identity
			}
		}
		for _, unit := range snapshot.Units {
			label := snapshot.Document.Units[unit.ID].Label
			if label == "" {
				label = unit.ID
			}
			identity, acquired := identities[unit.ID]
			view.Units = append(view.Units, RunUnitView{
				ID: unit.ID, Label: label, Kind: unit.Kind,
				LogsAvailable: acquired && s.logs != nil && s.logs.HasLog(identity),
			})
		}
		result = append(result, view)
	}
	return result, nil
}

// ReadLog resolves the original process through the current workspace journal.
// The renderer may select a unit but cannot supply a process handle or a path.
func (s *RunHistoryService) ReadLog(ctx context.Context, projectID ProjectID, runID RunID, unitID string, after EventCursor, limit int) (run.LogPage, error) {
	s.workspace.mu.RLock()
	defer s.workspace.mu.RUnlock()
	store, err := s.repository()
	if err != nil {
		return run.LogPage{}, err
	}
	record, err := store.Run(ctx, string(runID))
	if err != nil {
		return run.LogPage{}, err
	}
	if record.Snapshot.ID != string(runID) || record.Snapshot.ProfileRef.ProjectID != projectID {
		return run.LogPage{}, problem.New("RUN_MISSING", "")
	}
	if limit < 1 || limit > 2000 {
		return run.LogPage{}, problem.New("LOG_QUERY_INVALID", "")
	}
	if s.logs != nil {
		for _, acquisition := range record.Acquisitions {
			if acquisition.UnitID == unitID && acquisition.Identity.Kind == "process" && s.logs.HasLog(acquisition.Identity) {
				return s.logs.ReadLog(ctx, acquisition.Identity, string(after), limit)
			}
		}
	}
	return run.LogPage{}, problem.New("RUN_LOG_UNAVAILABLE", "")
}

// repository is called under the workspace read lock. An unverified copy cannot
// use origin identities as evidence for the currently opened workspace.
func (s *RunHistoryService) repository() (ports.RunHistoryRepository, error) {
	if s.workspace.current == nil {
		return nil, problem.New("WORKSPACE_REQUIRED", "")
	}
	if s.workspace.current.Info().State != "ready" {
		return nil, problem.New("WORKSPACE_REBIND_REQUIRED", "")
	}
	store, ok := s.workspace.current.(ports.RunHistoryRepository)
	if !ok {
		return nil, problem.New("RUN_STORAGE_UNAVAILABLE", "")
	}
	return store, nil
}
