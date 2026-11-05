// Package contract validates portable JSON using embedded, offline-only schemas.
package contract

import (
	"bytes"
	"embed"
	"encoding/json"
	"io"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

//go:embed schemas/*.schema.json
var schemas embed.FS

const maxDocumentBytes = 16 * 1024 * 1024

var projectSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	data, err := schemas.ReadFile("schemas/project.schema.json")
	if err != nil {
		return nil, err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineLoader{})
	const uri = "https://local-project-hub.invalid/contracts/v1/project.schema.json"
	if err := compiler.AddResource(uri, document); err != nil {
		return nil, err
	}
	return compiler.Compile(uri)
})

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) {
	return nil, problem.New("SCHEMA_REMOTE_REFERENCE_REJECTED", "")
}

// DecodeProject rejects ambiguous input before schema validation, then checks
// relationships. Error text does not include input values that might be secrets.
func DecodeProject(data []byte) (project.Document, error) {
	var result project.Document
	if len(data) > maxDocumentBytes || !utf8.Valid(data) {
		return result, problem.New("PROJECT_DOCUMENT_INVALID", "")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanValue(decoder, 0); err != nil {
		return result, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return result, problem.New("JSON_TRAILING_DATA", "")
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return result, problem.New("JSON_INVALID", "")
	}
	schema, err := projectSchema()
	if err != nil {
		return result, problem.New("SCHEMA_UNAVAILABLE", "")
	}
	if err := schema.Validate(value); err != nil {
		return result, problem.New("PROJECT_SCHEMA_INVALID", "")
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, problem.New("PROJECT_DOCUMENT_INVALID", "")
	}
	if err := project.Validate(result); err != nil {
		return project.Document{}, err
	}
	return result, nil
}

// scanValue bounds nesting and detects duplicate object keys before encoding/json
// can silently overwrite earlier declarations.
func scanValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return problem.New("JSON_DEPTH_LIMIT", "")
	}
	token, err := decoder.Token()
	if err != nil {
		return problem.New("JSON_INVALID", "")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return problem.New("JSON_INVALID", "")
			}
			key, ok := token.(string)
			if !ok {
				return problem.New("JSON_INVALID", "")
			}
			if keys[key] {
				return problem.New("JSON_DUPLICATE_KEY", "")
			}
			keys[key] = true
			if err := scanValue(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanValue(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return problem.New("JSON_INVALID", "")
	}
	if _, err := decoder.Token(); err != nil {
		return problem.New("JSON_INVALID", "")
	}
	return nil
}
