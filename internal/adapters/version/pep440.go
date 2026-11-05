package version

import (
	"errors"
	"strings"

	pep440 "github.com/aquasecurity/go-pep440-version"
)

func matchPEP440(rangeText, observed string) (bool, error) {
	if strings.Contains(rangeText, "===") {
		return false, errors.New("arbitrary equality unsupported")
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
