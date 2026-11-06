package application

import (
	"context"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type observedLog struct {
	identity run.Identity
	reads    int
}

func (o *observedLog) HasLog(identity run.Identity) bool { return identity == o.identity }

func (o *observedLog) ReadLog(_ context.Context, identity run.Identity, _ string, _ int) (run.LogPage, error) {
	o.reads++
	if identity != o.identity {
		panic("renderer-selected identity reached the process adapter")
	}
	return run.LogPage{Entries: []run.LogEntry{{Text: "test output"}}}, nil
}

func TestRunHistoryScopesLogsToTheSelectedProjectAndRun(t *testing.T) {
	ctx := context.Background()
	ws, _, _, doc := lifecycleFixture(t)
	repo := ws.current.(ports.RunRepository)
	const runID = "33333333-3333-4333-8333-333333333333"
	snapshot := run.Snapshot{
		ID: runID, Document: doc,
		ProfileRef: project.Ref{ProjectID: project.ID(doc.ProjectID), ProfileID: "development", ExpectedRevision: 1},
		Units:      []run.UnitPlan{{ID: "api", Kind: "process"}},
	}
	if err := repo.CreateRun(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	record, err := repo.Run(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	id := run.Identity{Kind: "process", ControlID: "original"}
	record.Acquisitions = []run.Acquisition{{UnitID: "api", Identity: id, Phase: "released"}}
	record.State = "stopped"
	if err := repo.UpdateRun(ctx, record, record.Version); err != nil {
		t.Fatal(err)
	}
	log := &observedLog{identity: id}
	history := NewRunHistoryService(ws, log)
	runs, err := history.Runs(ctx, project.ID(doc.ProjectID))
	if err != nil || len(runs) != 1 || runs[0].Revision != 1 || runs[0].State != "stopped" ||
		len(runs[0].Units) != 1 || !runs[0].Units[0].LogsAvailable {
		t.Fatalf("stopped run and its available log must remain listed: %+v, %v", runs, err)
	}
	if _, err := history.ReadLog(ctx, "another-project", runID, "api", "", 10); err == nil {
		t.Fatal("read a log from a different project")
	}
	if _, err := history.ReadLog(ctx, project.ID(doc.ProjectID), runID, "wrong-unit", "", 10); err == nil {
		t.Fatal("read a log from a different unit")
	}
	if log.reads != 0 {
		t.Fatalf("invalid query reached adapter %d times", log.reads)
	}
	page, err := history.ReadLog(ctx, project.ID(doc.ProjectID), runID, "api", "", 10)
	if err != nil || len(page.Entries) != 1 || log.reads != 1 {
		t.Fatalf("valid log read: %+v, %v", page, err)
	}
}
