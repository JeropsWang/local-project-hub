package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/workspace"
)

var workspaceSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	data, err := schemas.ReadFile("schemas/workspace.schema.json")
	if err != nil {
		return nil, err
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineLoader{})
	const uri = "https://local-project-hub.invalid/contracts/v1/workspace.schema.json"
	if err := compiler.AddResource(uri, value); err != nil {
		return nil, err
	}
	return compiler.Compile(uri)
})

// DecodeWorkspace checks identity before any database is opened or modified.
func DecodeWorkspace(data []byte) (workspace.Document, error) {
	var result workspace.Document
	if len(data) > 1024*1024 || !utf8.Valid(data) {
		return result, problem.New("WORKSPACE_IDENTITY_INVALID", "")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanValue(decoder, 0); err != nil {
		return result, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return result, problem.New("WORKSPACE_IDENTITY_INVALID", "")
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return result, problem.New("WORKSPACE_IDENTITY_INVALID", "")
	}
	// Read the version only after rejecting duplicate fields.
	var version struct {
		SchemaVersion string `json:"schemaVersion"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return result, problem.New("WORKSPACE_IDENTITY_INVALID", "")
	}
	if version.SchemaVersion != "1.0" {
		return result, problem.New("WORKSPACE_VERSION_UNSUPPORTED", "")
	}
	schema, err := workspaceSchema()
	if err != nil {
		return result, problem.New("SCHEMA_UNAVAILABLE", "")
	}
	if err := schema.Validate(value); err != nil {
		return result, problem.New("WORKSPACE_IDENTITY_INVALID", "")
	}
	if err := json.Unmarshal(data, &result); err != nil || result.CreatedAt.IsZero() {
		return result, problem.New("WORKSPACE_IDENTITY_INVALID", "")
	}
	return result, nil
}
