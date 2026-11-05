// Package control defines the bounded local managed-control protocol independently of transport.
package control

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"unicode/utf8"
)

const MaxFrame = 16 * 1024

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var tokenText = regexp.MustCompile(`^[0-9a-f]{64}$`)
var errorCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// Request contains only protocol operations; it cannot execute a command.
type Request struct {
	ProtocolVersion string `json:"protocolVersion"`
	RunID           string `json:"runId"`
	OperationID     string `json:"operationId"`
	Action          string `json:"action"`
	Token           string `json:"token"`
}

// Error is controlled application feedback. Clients must not publish raw messages as logs.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Response distinguishes acceptance from a completed operation and final state.
type Response struct {
	ProtocolVersion string `json:"protocolVersion"`
	RunID           string `json:"runId"`
	OperationID     string `json:"operationId"`
	Kind            string `json:"kind"`
	State           string `json:"state"`
	Error           *Error `json:"error,omitempty"`
}

// ValidIdentity accepts only canonical per-run UUIDs used to derive a local pipe name.
func ValidIdentity(id string) bool { return uuid.MatchString(id) }

// ValidToken checks the exact text representation before constant-time authentication.
func ValidToken(token string) bool { return tokenText.MatchString(token) }

// Authenticate avoids early-exit comparison of equally sized runtime tokens.
func Authenticate(actual, expected string) bool {
	return ValidToken(actual) && ValidToken(expected) &&
		subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

// DecodeRequest rejects duplicate/unknown fields, trailing data and invalid identities.
func DecodeRequest(data []byte) (Request, error) {
	var result Request
	if err := strictMessage(data, &result, []string{"protocolVersion", "runId", "operationId", "action", "token"}); err != nil {
		return result, err
	}
	if result.ProtocolVersion != "1.0" || !ValidIdentity(result.RunID) || !ValidIdentity(result.OperationID) ||
		!ValidToken(result.Token) ||
		!slices.Contains([]string{"status", "quiesce", "shutdown"}, result.Action) {
		return Request{}, errors.New("INVALID_REQUEST")
	}
	return result, nil
}

// DecodeResponse validates association before a client can treat the response as evidence.
func DecodeResponse(data []byte, runID, operationID string) (Response, error) {
	var result Response
	if err := strictMessage(data, &result, []string{"protocolVersion", "runId", "operationId", "kind", "state", "error"}); err != nil {
		return result, err
	}
	if result.ProtocolVersion != "1.0" || result.RunID != runID || result.OperationID != operationID ||
		!ValidIdentity(runID) ||
		!ValidIdentity(operationID) ||
		!slices.Contains([]string{"accepted", "status", "completed", "rejected"}, result.Kind) ||
		!slices.Contains([]string{"running", "quiescing", "blocked", "saving", "stopped", "failed"}, result.State) {
		return Response{}, errors.New("INVALID_RESPONSE")
	}
	if result.Error != nil && (!errorCode.MatchString(result.Error.Code) || len(result.Error.Message) > 2048) {
		return Response{}, errors.New("INVALID_RESPONSE")
	}
	if (result.Kind == "rejected" || result.State == "failed" || result.State == "blocked") && result.Error == nil {
		return Response{}, errors.New("INVALID_RESPONSE")
	}
	return result, nil
}

func strictMessage(data []byte, target any, fields []string) error {
	if len(data) > MaxFrame || !utf8.Valid(data) {
		return errors.New("INVALID_FRAME")
	}
	if err := uniqueObject(data, fields); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("TRAILING_DATA")
	}
	return nil
}

// Only two flat object levels exist in v1. Nulls, arrays and nested arbitrary payloads
// are rejected before encoding/json can silently overwrite duplicate fields.
func uniqueObject(data []byte, fields []string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return errors.New("OBJECT_REQUIRED")
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok || seen[key] || !slices.Contains(fields, key) {
			return errors.New("INVALID_FIELD")
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		if key == "error" {
			if err := uniqueObject(value, []string{"code", "message"}); err != nil {
				return err
			}
		} else {
			var text string
			if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &text) != nil {
				return errors.New("STRING_REQUIRED")
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	for _, field := range fields {
		if field != "error" && !seen[field] {
			return errors.New("MISSING_FIELD")
		}
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("TRAILING_DATA")
	}
	return nil
}
