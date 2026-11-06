package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
)

var bundleSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	data, err := schemas.ReadFile("schemas/bundle.schema.json")
	if err != nil {
		return nil, err
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineLoader{})
	const uri = "https://local-project-hub.invalid/contracts/v1/bundle.schema.json"
	if err = compiler.AddResource(uri, value); err != nil {
		return nil, err
	}
	return compiler.Compile(uri)
})

// DecodeManifest validates the closed, offline package schema.
func DecodeManifest(data []byte) (bundle.Manifest, error) {
	var result bundle.Manifest
	if len(data) > bundle.MaxManifest || !utf8.Valid(data) {
		return result, problem.New("BUNDLE_MANIFEST_INVALID", "")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanValue(decoder, 0); err != nil {
		return result, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return result, problem.New("JSON_TRAILING_DATA", "")
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, problem.New("BUNDLE_MANIFEST_INVALID", "")
	}
	if result.BundleVersion != bundle.Version {
		return result, problem.New("BUNDLE_VERSION_UNSUPPORTED", "")
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return result, err
	}
	schema, err := bundleSchema()
	if err != nil {
		return result, err
	}
	if err = schema.Validate(value); err != nil {
		return result, problem.New("BUNDLE_MANIFEST_INVALID", "")
	}
	return result, nil
}
