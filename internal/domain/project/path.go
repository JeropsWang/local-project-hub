package project

import (
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"golang.org/x/text/unicode/norm"
	"regexp"
	"strings"
	"unicode"
)

// Path refers to a project-scoped local binding.
type Path struct {
	Binding      string `json:"binding"`
	RelativePath string `json:"relativePath"`
}

// File distinguishes included bytes, external requirements and generated outputs.
type File struct {
	Availability    string `json:"availability"`
	BundlePath      string `json:"bundlePath,omitempty"`
	Path            *Path  `json:"path,omitempty"`
	Required        bool   `json:"required"`
	Role            string `json:"role"`
	ExpectedSHA256  string `json:"expectedSha256,omitempty"`
	GeneratorUnitID string `json:"generatorUnitId,omitempty"`
}

var deviceName = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³])$`)

// ValidateRelativePath performs portable lexical checks. The filesystem adapter
// must still reject reparse points and enforce containment when opening files.
func ValidateRelativePath(value string) error {
	if value == "" || len(value) > 1024 || !norm.NFC.IsNormalString(value) ||
		strings.HasPrefix(value, "/") || strings.ContainsAny(value, `\:<>"|?*`) {
		return problem.New("INVALID_PATH", "")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return problem.New("INVALID_PATH", "")
		}
	}
	if value == "." {
		return nil
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || strings.TrimRight(part, ". ") != part {
			return problem.New("INVALID_PATH", "")
		}
		base, _, _ := strings.Cut(part, ".")
		if deviceName.MatchString(base) {
			return problem.New("INVALID_PATH", "")
		}
	}
	return nil
}
