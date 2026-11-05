package environment

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func TestRunCompilerFreezesScriptsAndKeepsSecretsAsReferences(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	for name, data := range map[string]string{"python.exe": "synthetic executable", "stop.py": "print('original')\n", "entry.py": "print('entry')\n"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := project.Command{
		Executable:  project.Executable{Kind: "runtime", RequirementID: "python", RelativePath: "python.exe"},
		Args:        []string{"-I", "${file:entry}"},
		CWD:         project.Path{Binding: "source", RelativePath: "."},
		EnvBindings: map[string]string{"TOKEN": "credential"},
	}
	stop := command
	stop.Args = []string{"${file:stop}"}
	doc := project.Document{
		ProjectID:    "11111111-1111-4111-8111-111111111111",
		Requirements: map[string]project.Requirement{"python": {Component: "python"}},
		Files: map[string]project.File{
			"entry": {
				Availability: "external",
				Path:         &project.Path{Binding: "source", RelativePath: "entry.py"},
				Role:         "source",
				Required:     true,
			},
			"stop": {
				Availability: "external",
				Path:         &project.Path{Binding: "source", RelativePath: "stop.py"},
				Role:         "script",
				Required:     true,
			},
		},
		Units: map[string]project.Unit{
			"api": {
				Kind:    "process",
				Control: "managed",
				Start:   &command,
				Stop:    &project.Stop{Kind: "command", Command: &stop, TimeoutSeconds: 2},
			},
		},
	}
	req := ports.InspectionRequest{
		Document:      doc,
		WorkspaceRoot: root,
		Revision:      1,
		UnitIDs:       []string{"api"},
		Bindings: map[string]project.Binding{
			"source": {
				Kind:         "path",
				LocalLocator: source,
			}, "python": {Kind: "runtime", LocalLocator: filepath.Join(source, "python.exe")}, "credential": {Kind: "secret", SecretRef: "dpapi:synthetic"},
		},
	}
	snapshot, err := (RunCompiler{}).CompileRun(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Units) != 1 || snapshot.Units[0].Start.SecretRefs["TOKEN"] != "dpapi:synthetic" {
		t.Fatal("secret reference lost")
	}
	if strings.Join(snapshot.Units[0].Start.Args, ",") != "-B,-I,"+filepath.Join(source, "entry.py") {
		t.Fatal("Python launch is not read-only", snapshot.Units[0].Start.Args)
	}
	if snapshot.Units[0].Start.Environment["GOCACHE"] == "" ||
		!strings.HasPrefix(snapshot.Units[0].Start.Environment["npm_config_cache"], root) {
		t.Fatal("global caches inherited")
	}
	if err = os.WriteFile(filepath.Join(source, "stop.py"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if string(snapshot.ScriptBytes["stop"]) != "print('original')\n" || snapshot.Units[0].Stop.Args[0] != "-B" ||
		snapshot.Units[0].Stop.Args[1] != "${file:stop}" {
		t.Fatal("stop did not freeze")
	}
	req.Document.Units["api"].Start.EnvBindings["GOTOOLCHAIN"] = "credential"
	if _, err = (RunCompiler{}).CompileRun(context.Background(), req); err == nil ||
		err.Error() != "ENVIRONMENT_VALUE_MANAGED" {
		t.Fatal("project overwrote managed runtime/cache policy", err)
	}
	delete(req.Document.Units["api"].Start.EnvBindings, "GOTOOLCHAIN")
	req.Document.Units["api"].Start.CWD.RelativePath = "../escape"
	if _, err = (RunCompiler{}).CompileRun(context.Background(), req); err == nil {
		t.Fatal("cwd escaped binding")
	}
}
