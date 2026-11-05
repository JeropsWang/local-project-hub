package project

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func fixture(t *testing.T) Document {
	t.Helper()
	data, err := os.ReadFile("../../../docs/contracts/examples/demo-bundle/project.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestOrderExpandsDependencies(t *testing.T) {
	doc := fixture(t)
	profile := doc.Profiles["development"]
	profile.UnitIDs = []string{"api"}
	doc.Profiles["development"] = profile
	order, err := Order(doc, "development")
	if err != nil || !reflect.DeepEqual(order, []string{"database", "api"}) {
		t.Fatalf("order %v, error %v", order, err)
	}
}

func TestOrderRejectsCycleAndMissingStop(t *testing.T) {
	doc := fixture(t)
	database := doc.Units["database"]
	database.DependsOn = []string{"api"}
	doc.Units["database"] = database
	if _, err := Order(doc, "development"); err == nil || err.Error() != "DEPENDENCY_CYCLE" {
		t.Fatalf("expected dependency cycle, got %v", err)
	}
	doc = fixture(t)
	api := doc.Units["api"]
	api.Stop = nil
	doc.Units["api"] = api
	if err := Validate(doc); err == nil {
		t.Fatal("missing stop accepted")
	}
}

func TestValidateRejectsBrokenReferencesAndUnsafeBindings(t *testing.T) {
	cases := map[string]func(*Document){
		"missing requirement": func(d *Document) { delete(d.Requirements, "node-runtime") },
		"missing file":        func(d *Document) { delete(d.Files, "stop-entry") },
		"missing dependency":  func(d *Document) { delete(d.Units, "database") },
		"unselected runtime":  func(d *Document) { u := d.Units["api"]; u.RequirementIDs = []string{}; d.Units["api"] = u },
		"nil features do not enable dependencies": func(d *Document) {
			u := d.Units["database"]
			u.FeatureID = "search"
			d.Units["database"] = u
			p := d.Profiles["development"]
			p.FeatureIDs = nil
			d.Profiles["development"] = p
		},
		"reserved control variable": func(d *Document) { d.Units["api"].Start.EnvBindings["LPH_CONTROL_TOKEN"] = "source" },
		"env case collision":        func(d *Document) { d.Units["api"].Start.EnvBindings["database_url"] = "source" },
		"path escape":               func(d *Document) { d.Units["api"].Start.CWD.RelativePath = "../outside" },
		"undeclared placeholder":    func(d *Document) { d.Units["api"].Start.Args = []string{"${secret:token}"} },
		"feature disabled":          func(d *Document) { u := d.Units["database"]; u.FeatureID = "search"; d.Units["database"] = u },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			doc := fixture(t)
			mutate(&doc)
			if err := Validate(doc); err == nil {
				t.Fatal("invalid relationships accepted")
			}
		})
	}
}

func TestIncludedFilesRejectWindowsAliases(t *testing.T) {
	doc := fixture(t)
	doc.Files["first"] = File{Availability: "included", BundlePath: "files/Config.json", Role: "config"}
	doc.Files["second"] = File{Availability: "included", BundlePath: "files/config.json", Role: "config"}
	if err := Validate(doc); err == nil {
		t.Fatal("case-insensitive bundle paths collide")
	}
}

func TestPortablePathsRejectWindowsEscapes(t *testing.T) {
	for _, value := range []string{"../x", "C:/x", "file:stream", "CON.txt", "x./x", "//host/share", `a\b`, "a//b", "a/./b", "a\x00b", "files/e\u0301.txt"} {
		if err := ValidateRelativePath(value); err == nil {
			t.Fatalf("unsafe path accepted: %q", value)
		}
	}
}
