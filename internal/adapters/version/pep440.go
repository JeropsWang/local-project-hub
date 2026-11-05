package version

import (
	"errors"
	"regexp"
	"strings"

	pep440 "github.com/aquasecurity/go-pep440-version"
)

var pepClause = regexp.MustCompile(`^(~=|==|!=|<=|>=|<|>)\s*([A-Za-z0-9.!+_*\-]+)$`)
var pepWildcard = regexp.MustCompile(`^(?:\d+!)?\d+(?:\.\d+)*\.\*$`)

func matchPEP440(rangeText, observed string) (bool, error) {
	// The library accepts extensions (bare versions, '=' and '||'); those are
	// not PEP 440 specifier sets and must not silently certify compatibility.
	if strings.ContainsAny(rangeText, "|") {
		return false, errors.New("unsupported PEP 440 syntax")
	}
	for _, clause := range strings.Split(rangeText, ",") {
		match := pepClause.FindStringSubmatch(strings.TrimSpace(clause))
		if match == nil {
			return false, errors.New("unsupported PEP 440 syntax")
		}
		if strings.Contains(match[2], "*") && ((match[1] != "==" && match[1] != "!=") || !pepWildcard.MatchString(match[2])) {
			return false, errors.New("unsupported wildcard specifier")
		}
		if match[1] == "~=" && !strings.Contains(match[2], ".") {
			return false, errors.New("compatible release requires two components")
		}
	}
	specifiers, err := pep440.NewSpecifiers(rangeText)
	if err != nil {
		return false, err
	}
	value, err := pep440.Parse(observed)
	if err != nil {
		return false, err
	}
	return specifiers.Check(value), nil
}
