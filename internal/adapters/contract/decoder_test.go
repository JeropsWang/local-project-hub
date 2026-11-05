package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func example(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../../docs/contracts/examples/demo-bundle/project.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Removing strict decoding must expose either duplicate identities or invented local authority.
func TestDecodeRejectsDuplicateKeysAndUnknownVersion(t *testing.T) {
	data := example(t)
	cases := map[string][]byte{
		"duplicate":         bytes.Replace(data, []byte(`"schemaVersion": "1.0"`), []byte(`"schemaVersion":"1.0","schemaVersion":"1.0"`), 1),
		"version":           bytes.Replace(data, []byte(`"schemaVersion": "1.0"`), []byte(`"schemaVersion":"9.0"`), 1),
		"authority":         bytes.Replace(data, []byte(`"schemaVersion": "1.0"`), []byte(`"controlGrant":true,"schemaVersion":"1.0"`), 1),
		"nested duplicate":  bytes.Replace(data, []byte(`"kind": "provided"`), []byte(`"kind":"provided","kind":"provided"`), 1),
		"trailing document": append(append([]byte{}, data...), []byte(`{}`)...),
		"oversize":          bytes.Repeat([]byte(" "), 16*1024*1024+1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeProject(input); err == nil {
				t.Fatal("invalid document accepted")
			}
		})
	}
}

func TestDecodeExampleAndMissingStop(t *testing.T) {
	doc, err := DecodeProject(example(t))
	if err != nil || doc.Name != "示例本地 Web 项目" {
		t.Fatalf("valid example: %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(example(t), &value); err != nil {
		t.Fatal(err)
	}
	delete(value["units"].(map[string]any)["api"].(map[string]any), "stop")
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeProject(data); err == nil {
		t.Fatal("process without stop accepted")
	}
}

func TestEmbeddedSchemasMatchContracts(t *testing.T) {
	for _, name := range []string{"project", "workspace", "bundle"} {
		actual, err := schemas.ReadFile("schemas/" + name + ".schema.json")
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile("../../../docs/contracts/" + name + ".schema.json")
		if err != nil || !bytes.Equal(actual, want) {
			t.Fatalf("schema %s differs from public contract: %v", name, err)
		}
	}
}
