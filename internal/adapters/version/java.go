package version

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var javaVersion = regexp.MustCompile(`^(?:1\.)?(\d+)(?:[._+\-].*)?$`)
var javaComparator = regexp.MustCompile(`^(>=|<=|>|<|==|=)?(\d+)$`)

func matchJava(rangeText, observed string) (bool, error) {
	match := javaVersion.FindStringSubmatch(observed)
	if match == nil {
		return false, errors.New("unknown Java version")
	}
	actual, _ := strconv.Atoi(match[1])
	comparators := strings.Fields(rangeText)
	if len(comparators) == 0 {
		return false, errors.New("missing Java requirement")
	}
	for _, text := range comparators {
		match := javaComparator.FindStringSubmatch(text)
		if match == nil {
			return false, errors.New("unsupported Java range")
		}
		expected, _ := strconv.Atoi(match[2])
		switch match[1] {
		case "", "=", "==":
			if actual != expected {
				return false, nil
			}
		case ">=":
			if actual < expected {
				return false, nil
			}
		case ">":
			if actual <= expected {
				return false, nil
			}
		case "<=":
			if actual > expected {
				return false, nil
			}
		case "<":
			if actual >= expected {
				return false, nil
			}
		}
	}
	return true, nil
}

// JavaRequirements distinguishes compiler requirements from JVM artifact requirements.
type JavaRequirements struct {
	BuildJDKMinimum int
	RuntimeMinimum  int
}

// ClassRuntimeMinimum reads the classfile header without executing Java.
func ClassRuntimeMinimum(data []byte) (int, error) {
	if len(data) < 8 || binary.BigEndian.Uint32(data[:4]) != 0xcafebabe {
		return 0, errors.New("invalid class header")
	}
	major := int(binary.BigEndian.Uint16(data[6:8]))
	if major < 45 || major > 100 {
		return 0, errors.New("unsupported classfile version")
	}
	return major - 44, nil
}

// ParseMavenRequirements supports literal compiler release/source/target properties.
// Parent inheritance, profiles and dynamic interpolation remain unknown, not guessed.
func ParseMavenRequirements(data []byte) (JavaRequirements, error) {
	result := JavaRequirements{}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	path := []string{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			path = append(path, value.Name.Local)
		case xml.EndElement:
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		case xml.CharData:
			if len(path) < 2 {
				continue
			}
			key := path[len(path)-1]
			if key != "maven.compiler.release" && key != "maven.compiler.source" && key != "maven.compiler.target" {
				continue
			}
			text := strings.TrimSpace(string(value))
			if text == "" {
				continue
			}
			version := javaVersion.FindStringSubmatch(text)
			if version == nil {
				return result, errors.New("dynamic Maven compiler declaration")
			}
			release, _ := strconv.Atoi(version[1])
			if release > result.BuildJDKMinimum {
				result.BuildJDKMinimum = release
			}
			if key != "maven.compiler.source" && release > result.RuntimeMinimum {
				result.RuntimeMinimum = release
			}
		}
	}
	if result.RuntimeMinimum == 0 && result.BuildJDKMinimum != 0 {
		result.RuntimeMinimum = result.BuildJDKMinimum
	}
	if result.BuildJDKMinimum == 0 {
		return result, errors.New("Maven compiler requirement undeclared")
	}
	return result, nil
}
