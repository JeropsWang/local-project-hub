package version

import (
	"errors"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

var npmCharacters = regexp.MustCompile(`^[0-9A-Za-zxX*.+<>=~^| \t-]*$`)
var prereleaseTuple = regexp.MustCompile(`[vV]?\d+\.\d+\.\d+-[0-9A-Za-z.-]+`)

func matchNPM(rangeText, observed string) (bool, error) {
	// Masterminds also accepts syntax that npm rejects; reject those extensions.
	if !npmCharacters.MatchString(rangeText) || strings.Contains(rangeText, "~>") ||
		strings.Contains(rangeText, "=>") || strings.Contains(rangeText, "=<") || strings.Contains(rangeText, "===") {
		return false, errors.New("unsupported npm range")
	}
	value, err := semver.StrictNewVersion(strings.TrimPrefix(observed, "v"))
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(rangeText) == "" {
		rangeText = "*"
	}
	alternatives := strings.Split(rangeText, "||")
	constraints := make([]*semver.Constraints, 0, len(alternatives))
	// Validate the whole expression before any branch can return success.
	for _, alternative := range alternatives {
		if strings.TrimSpace(alternative) == "" {
			alternative = "*"
		}
		constraint, err := semver.NewConstraint(alternative)
		if err != nil {
			return false, err
		}
		constraints = append(constraints, constraint)
	}
	for i, alternative := range alternatives {
		constraint := constraints[i]
		if value.Prerelease() != "" {
			// npm only admits a prerelease when a comparator in this OR branch
			// explicitly names a prerelease with the same major/minor/patch tuple.
			allowed := false
			for _, candidate := range prereleaseTuple.FindAllString(alternative, -1) {
				pre, err := semver.StrictNewVersion(strings.TrimLeft(candidate, "vV"))
				if err == nil && pre.Major() == value.Major() && pre.Minor() == value.Minor() && pre.Patch() == value.Patch() {
					allowed = true
				}
			}
			if !allowed {
				continue
			}
		}
		if constraint.Check(value) {
			return true, nil
		}
	}
	return false, nil
}
