package version

import (
	"errors"
	goversion "go/version"
	"regexp"
	"strings"

	"golang.org/x/mod/modfile"
)

var goMinimum = regexp.MustCompile(`^(?:>=\s*)?(?:go)?(\d+\.\d+(?:\.\d+)?)$`)

func matchGo(rangeText, observed string) (bool, error) {
	match := goMinimum.FindStringSubmatch(strings.TrimSpace(rangeText))
	actual := "go" + strings.TrimPrefix(observed, "go")
	if match == nil || !goversion.IsValid(actual) {
		return false, errors.New("unsupported Go version constraint")
	}
	return goversion.Compare(actual, "go"+match[1]) >= 0, nil
}

// GoRequirements separates the minimum language/build version from preferred toolchain.
type GoRequirements struct {
	Minimum   string
	Toolchain string
}

// ParseGoModule reads go.mod/go.work without invoking Go or automatic downloads.
func ParseGoModule(name string, data []byte) (GoRequirements, error) {
	result := GoRequirements{}
	if name == "go.work" {
		work, err := modfile.ParseWork(name, data, nil)
		if err != nil {
			return result, err
		}
		if work.Go != nil {
			result.Minimum = work.Go.Version
		}
		if work.Toolchain != nil {
			result.Toolchain = strings.TrimPrefix(work.Toolchain.Name, "go")
		}
	} else if name == "go.mod" {
		mod, err := modfile.Parse(name, data, nil)
		if err != nil {
			return result, err
		}
		if mod.Go != nil {
			result.Minimum = mod.Go.Version
		}
		if mod.Toolchain != nil {
			result.Toolchain = strings.TrimPrefix(mod.Toolchain.Name, "go")
		}
	} else {
		return result, errors.New("unsupported Go declaration")
	}
	if result.Minimum == "" {
		return result, errors.New("Go minimum undeclared")
	}
	return result, nil
}
