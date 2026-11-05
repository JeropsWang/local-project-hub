package version

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func requirement(semantics, rangeText string) project.Requirement {
	return project.Requirement{Component: "node", Semantics: semantics, Range: rangeText, Architecture: "any", Evidence: project.RequirementEvidence{Kind: "declaration", Reference: "synthetic"}}
}

func TestLockedNodeEngines(t *testing.T) {
	data, err := os.ReadFile("../../../tests/fixtures/compatibility/npm.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Range, Version   string
			Valid, Satisfied bool
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, test := range fixture.Cases {
		got, err := Evaluate(requirement("npm", test.Range), ports.Observation{Version: test.Version})
		if err != nil {
			t.Fatal(err)
		}
		want := check.Incompatible
		if !test.Valid {
			want = check.Unknown
		} else if test.Satisfied {
			want = check.Satisfied
		}
		if got.Status != want {
			t.Errorf("%q @ %s: %s, want %s", test.Range, test.Version, got.Status, want)
		}
	}
}

func TestUnsupportedPythonAndABI(t *testing.T) {
	runFixture(t, "python", "pep440")
}
func TestGoAndJavaUseTheirOwnVersionRules(t *testing.T) {
	runFixture(t, "go", "goversion")
	runFixture(t, "java", "java")
}

func runFixture(t *testing.T, name, semantics string) {
	t.Helper()
	data, err := os.ReadFile("../../../tests/fixtures/compatibility/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var tests []struct{ Range, Version, ABI, ObservedABI, Expected string }
	if err := json.Unmarshal(data, &tests); err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		req := requirement(semantics, test.Range)
		req.Component = name
		req.ABI = test.ABI
		got, err := Evaluate(req, ports.Observation{Version: test.Version, ABI: test.ObservedABI})
		if err != nil || string(got.Status) != test.Expected {
			t.Errorf("%s %q @ %s: %+v, %v", name, test.Range, test.Version, got, err)
		}
	}
}

func TestEvidenceDoesNotPromoteCIToRuntime(t *testing.T) {
	data, err := os.ReadFile("../../../tests/fixtures/compatibility/evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ DeclarationKinds, ForbiddenPromotions []string }
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, kind := range fixture.DeclarationKinds {
		req := requirement("npm", ">=22.19.0")
		req.Evidence.Kind = kind
		got, err := Evaluate(req, ports.Observation{Version: "22.19.0", Evidence: check.Evidence{Kind: "observation", Source: "synthetic"}})
		if err != nil || got.Status != check.Satisfied {
			t.Fatal(got, err)
		}
		for _, evidence := range got.Evidence {
			for _, forbidden := range fixture.ForbiddenPromotions {
				if evidence.Kind == forbidden {
					t.Fatal("fabricated runtime evidence")
				}
			}
		}
	}
}

func TestUnknownArchitectureAndCapabilitiesNeverPass(t *testing.T) {
	req := requirement("npm", ">=22.19.0")
	req.Architecture = "amd64"
	got, _ := Evaluate(req, ports.Observation{Version: "22.19.0"})
	if got.Status != check.Unknown {
		t.Fatal("missing architecture passed")
	}
	got, _ = Evaluate(req, ports.Observation{Version: "22.19.0", Architecture: "arm64"})
	if got.Status != check.Incompatible {
		t.Fatal("wrong architecture passed")
	}
	req.Architecture = "any"
	req.Capabilities = []string{"pgvector"}
	got, _ = Evaluate(req, ports.Observation{Version: "22.19.0"})
	if got.Status != check.Unknown {
		t.Fatal("unobserved capability passed")
	}
}

func TestFailureExplainsObservedAndRequiredVersion(t *testing.T) {
	req := requirement("npm", "^22.12.0 || ^24.0.0 || >=26.0.0")
	got, err := Evaluate(req, ports.Observation{Version: "23.0.0"})
	if err != nil || got.Status != check.Incompatible || len(got.Reasons) == 0 || !strings.Contains(got.Reasons[0].Message, "23.0.0") || !strings.Contains(got.Reasons[0].Message, req.Range) {
		t.Fatalf("unactionable version report: %+v %v", got, err)
	}
}

func TestDataServiceVersionsAndUnknownCapabilitiesStaySeparate(t *testing.T) {
	for _, test := range []struct{ component, want, actual string }{{"postgres", "17", "18"}, {"redis", "7", "8"}} {
		req := requirement("exact", test.want)
		req.Component = test.component
		got, _ := Evaluate(req, ports.Observation{Version: test.actual})
		if got.Status != check.Incompatible {
			t.Fatal("database major versions merged")
		}
	}
	req := requirement("exact", "17")
	req.Component = "postgres"
	req.Capabilities = []string{"pgvector"}
	got, _ := Evaluate(req, ports.Observation{Version: "17"})
	if got.Status != check.Unknown {
		t.Fatal("database version implied extension readiness")
	}
}

func TestParseGoModuleAndJavaArtifactRequirements(t *testing.T) {
	goReq, err := ParseGoModule("go.mod", []byte("module example.test/demo\n\ngo 1.26.0\n\ntoolchain go1.26.1\n"))
	if err != nil || goReq.Minimum != "1.26.0" || goReq.Toolchain != "1.26.1" {
		t.Fatal(goReq, err)
	}
	work, err := ParseGoModule("go.work", []byte("go 1.26.0\nuse ./api\n"))
	if err != nil || work.Minimum != "1.26.0" {
		t.Fatal(work, err)
	}
	class, err := ClassRuntimeMinimum([]byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 55})
	if err != nil || class != 11 {
		t.Fatal(class, err)
	}
	java, err := ParseMavenRequirements([]byte("<project><properties><maven.compiler.release>17</maven.compiler.release></properties></project>"))
	if err != nil || java.BuildJDKMinimum != 17 || java.RuntimeMinimum != 17 {
		t.Fatal(java, err)
	}
}
