package application_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/environment"
	"github.com/JeropsWang/local-project-hub/internal/application"
	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type boundedProbe struct{ active, max atomic.Int32 }

func (p *boundedProbe) Discover(ctx context.Context, req ports.ProbeRequest) (ports.Observation, error) {
	active := p.active.Add(1)
	defer p.active.Add(-1)
	for old := p.max.Load(); active > old && !p.max.CompareAndSwap(old, active); old = p.max.Load() {
	}
	select {
	case <-time.After(20 * time.Millisecond):
	case <-ctx.Done():
		return ports.Observation{}, ctx.Err()
	}
	return ports.Observation{Component: req.Tool, Version: "22.19.0", Architecture: "amd64", Status: check.Unknown}, nil
}

func TestCheckDoesNotTrustBindingRowWithoutWorkingDirectoryOrExecutable(t *testing.T) {
	ws, projects := services(t)
	ctx := context.Background()
	source := t.TempDir()
	doc := declaration(t, "11111111-1111-4111-8111-111111111111")
	doc.Requirements = map[string]project.Requirement{}
	doc.Files = map[string]project.File{}
	doc.Units = map[string]project.Unit{"build": {ResourceID: "33333333-3333-4333-8333-333333333333", Label: "build", DependsOn: []string{}, RequirementIDs: []string{}, Kind: "task", Control: "managed", Effect: "build", CancelPolicy: "wait", Command: &project.Command{Executable: project.Executable{Kind: "binding", Binding: "source", RelativePath: "missing.exe"}, Args: []string{}, CWD: project.Path{Binding: "source", RelativePath: "missing-directory"}, EnvBindings: map[string]string{}}}}
	doc.Profiles["development"] = project.Profile{Label: "dev", UnitIDs: []string{"build"}, FeatureIDs: []string{}}
	view, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc, Bindings: []project.Binding{{Key: "source", Kind: "path", LocalLocator: source}}})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewCheckService(ws, &boundedProbe{}, environment.StaticInspector{}, nil)
	report, err := service.Check(ctx, application.CheckRequest{ProfileRef: application.ProfileRef{ProjectID: application.ProjectID(doc.ProjectID), ProfileID: "development", ExpectedRevision: view.Revision}})
	if err != nil || report.Summary == "ready" {
		t.Fatalf("nonexistent invocation reported ready: %+v %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(source, "missing.exe")); !os.IsNotExist(err) {
		t.Fatal("detection created an executable")
	}
}

type noFiles struct{}

func (noFiles) Inspect(context.Context, ports.InspectionRequest) (ports.SourceInspection, error) {
	return ports.SourceInspection{Fingerprint: "synthetic-source", Items: []check.Item{}}, nil
}

func TestDetectionIsReadOnlyAndBounded(t *testing.T) {
	ws, projects := services(t)
	ctx := context.Background()
	id := "11111111-1111-4111-8111-111111111111"
	doc := declaration(t, id)
	u := doc.Units["api"]
	for i := 0; i < 12; i++ {
		key := fmt.Sprintf("runtime-%d", i)
		doc.Requirements[key] = doc.Requirements["node-runtime"]
		u.RequirementIDs = append(u.RequirementIDs, key)
	}
	doc.Units["api"] = u
	view, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc})
	if err != nil {
		t.Fatal(err)
	}
	probe := &boundedProbe{}
	service := application.NewCheckService(ws, probe, noFiles{}, nil)
	start := time.Now()
	report, err := service.Check(ctx, application.CheckRequest{ProfileRef: application.ProfileRef{ProjectID: application.ProjectID(id), ProfileID: "development", ExpectedRevision: view.Revision}})
	if err != nil {
		t.Fatal(err)
	}
	if probe.max.Load() > 4 || probe.max.Load() < 2 || time.Since(start) > time.Second {
		t.Fatal("discovery concurrency or budget")
	}
	if report.Summary == "ready" || report.SourceFingerprint != "synthetic-source" {
		t.Fatalf("unknown environment marked ready: %+v", report)
	}
	after, err := projects.Get(ctx, application.ProjectID(id))
	if err != nil || after.Revision != view.Revision || after.ContentHash != view.ContentHash {
		t.Fatal("detection changed registration")
	}
}

func TestCheckRejectsStaleRevisionAndDisabledFeature(t *testing.T) {
	ws, projects := services(t)
	ctx := context.Background()
	doc := declaration(t, "11111111-1111-4111-8111-111111111111")
	view, err := projects.Register(ctx, application.RegisterProjectRequest{Document: doc})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewCheckService(ws, &boundedProbe{}, noFiles{}, nil)
	ref := application.ProfileRef{ProjectID: application.ProjectID(doc.ProjectID), ProfileID: "development", ExpectedRevision: view.Revision + 1}
	if _, err := service.Check(ctx, application.CheckRequest{ProfileRef: ref}); err == nil || err.Error() != "REVISION_CONFLICT" {
		t.Fatalf("stale check accepted: %v", err)
	}
	ref.ExpectedRevision = view.Revision
	if _, err := service.Check(ctx, application.CheckRequest{ProfileRef: ref, EnabledFeatureIDs: []string{"search"}}); err == nil || err.Error() != "PROFILE_FEATURES_UNSAVED" {
		t.Fatalf("unsaved feature override accepted: %v", err)
	}
}
