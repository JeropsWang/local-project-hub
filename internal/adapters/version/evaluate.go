// Package version evaluates each ecosystem without treating all versions as SemVer.
package version

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// Evaluate returns unknown for unsupported syntax or missing observations.
// A satisfied result concerns metadata only, never a runtime or desktop test.
func Evaluate(req project.Requirement, observation ports.Observation) (check.Item, error) {
	result := check.Item{Status: check.Unknown, Evidence: []check.Evidence{{Kind: "declaration", Source: req.Evidence.Reference}}, Reasons: []check.Reason{}, Repairs: []check.RepairStep{}}
	if observation.Evidence.Kind == "metadata" || observation.Evidence.Kind == "observation" {
		result.Evidence = append(result.Evidence, observation.Evidence)
	}
	reject := func(status check.Status, code string) (check.Item, error) {
		result.Status = status
		message := "The declared requirement has not been met or verified."
		if code == "VERSION_RANGE_MISMATCH" {
			message = fmt.Sprintf("Observed %s %s does not satisfy %s.", req.Component, observation.Version, req.Range)
		}
		if code == "ARCHITECTURE_MISMATCH" {
			message = fmt.Sprintf("Observed architecture %s; required %s.", observation.Architecture, req.Architecture)
		}
		if code == "ABI_MISMATCH" {
			message = fmt.Sprintf("Observed ABI %s; required %s.", observation.ABI, req.ABI)
		}
		result.Reasons = append(result.Reasons, check.Reason{Code: code, Subject: req.Component, Message: message})
		automatic := req.Component == "node" || req.Component == "npm" || req.Component == "go" || req.Component == "python"
		result.Repairs = append(result.Repairs, check.RepairStep{ID: "repair-" + req.Component, Label: "Select a compatible runtime or prepare a supported isolated environment.", Effect: "prepare", AutomaticSupported: automatic})
		return result, nil
	}
	if utf8.RuneCountInString(req.Range) > 512 {
		return reject(check.Unknown, "VERSION_SYNTAX_UNSUPPORTED")
	}
	if observation.Status == check.Missing {
		return reject(check.Missing, "TOOL_MISSING")
	}
	if observation.Status == check.BindingRequired {
		return reject(check.BindingRequired, "ENVIRONMENT_BINDING_REQUIRED")
	}
	if observation.ReasonCode != "" || observation.Version == "" {
		code := observation.ReasonCode
		if code == "" {
			code = "VERSION_UNOBSERVED"
		}
		return reject(check.Unknown, code)
	}
	var satisfied bool
	var err error
	switch req.Semantics {
	case "npm":
		satisfied, err = matchNPM(req.Range, observation.Version)
	case "pep440":
		satisfied, err = matchPEP440(req.Range, observation.Version)
	case "goversion":
		satisfied, err = matchGo(req.Range, observation.Version)
	case "java":
		satisfied, err = matchJava(req.Range, observation.Version)
	case "exact":
		satisfied, err = matchExact(req.Range, observation.Version)
	default:
		return reject(check.Unknown, "VERSION_SEMANTICS_UNSUPPORTED")
	}
	if err != nil {
		return reject(check.Unknown, "VERSION_SYNTAX_UNSUPPORTED")
	}
	if !satisfied {
		return reject(check.Incompatible, "VERSION_RANGE_MISMATCH")
	}
	if req.Architecture != "" && req.Architecture != "any" {
		if observation.Architecture == "" {
			return reject(check.Unknown, "ARCHITECTURE_UNOBSERVED")
		}
		if req.Architecture != observation.Architecture {
			return reject(check.Incompatible, "ARCHITECTURE_MISMATCH")
		}
	}
	if req.ABI != "" {
		if observation.ABI == "" {
			return reject(check.Unknown, "ABI_UNOBSERVED")
		}
		if normalizeABI(req.ABI) != normalizeABI(observation.ABI) {
			return reject(check.Incompatible, "ABI_MISMATCH")
		}
	}
	for _, capability := range req.Capabilities {
		if !slices.Contains(observation.Capabilities, capability) {
			return reject(check.Unknown, "CAPABILITY_UNOBSERVED")
		}
	}
	result.Status = check.Satisfied
	return result, nil
}

func normalizeABI(value string) string {
	value = strings.TrimPrefix(strings.ToLower(value), "cpython-")
	value = strings.TrimPrefix(value, "cp")
	value, _, _ = strings.Cut(value, "-")
	return value
}
