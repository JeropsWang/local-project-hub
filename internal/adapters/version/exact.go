package version

import (
	"errors"
	"strings"
)

func matchExact(rangeText, observed string) (bool, error) {
	if rangeText == "" || strings.TrimSpace(rangeText) != rangeText || observed == "" {
		return false, errors.New("missing exact version")
	}
	return rangeText == observed, nil
}
