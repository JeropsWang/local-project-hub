package environment

import (
	"bytes"
	"errors"
	"net/mail"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/JeropsWang/local-project-hub/internal/ports"
)

// parsePythonManifest extracts static interpreter bounds without importing Python code.
// Dependency files lacking interpreter metadata remain unverified rather than guessed.
func parsePythonManifest(name string, data []byte) ([]ports.MetadataConstraint, error) {
	text := ""
	switch name {
	case "pyproject.toml":
		var doc struct {
			Project struct {
				RequiresPython string   `toml:"requires-python"`
				Dynamic        []string `toml:"dynamic"`
			} `toml:"project"`
			Tool struct {
				Poetry struct {
					Dependencies struct {
						Python string `toml:"python"`
					} `toml:"dependencies"`
				} `toml:"poetry"`
			} `toml:"tool"`
		}
		if err := toml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		for _, dynamic := range doc.Project.Dynamic {
			if dynamic == "requires-python" {
				return nil, errors.New("dynamic Python requirement")
			}
		}
		text = doc.Project.RequiresPython
		if text == "" {
			text = doc.Tool.Poetry.Dependencies.Python
		}
	case "uv.lock":
		var doc struct {
			RequiresPython string `toml:"requires-python"`
		}
		if err := toml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		text = doc.RequiresPython
	case "poetry.lock":
		var doc struct {
			Metadata struct {
				PythonVersions string `toml:"python-versions"`
			} `toml:"metadata"`
		}
		if err := toml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		text = doc.Metadata.PythonVersions
	case "METADATA", "PKG-INFO":
		message, err := mail.ReadMessage(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		text = message.Header.Get("Requires-Python")
	default:
		if strings.HasPrefix(name, "requirements") && strings.HasSuffix(name, ".txt") {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					return nil, errors.New("dependency interpreter metadata unverified")
				}
			}
		}
	}
	if text == "" || text == "*" {
		return []ports.MetadataConstraint{}, nil
	}
	return []ports.MetadataConstraint{{Component: "python", Semantics: "pep440", Range: text}}, nil
}

func isPythonManifest(name string) bool {
	switch name {
	case "pyproject.toml", "uv.lock", "poetry.lock", "METADATA", "PKG-INFO":
		return true
	}
	return strings.HasPrefix(name, "requirements") && strings.HasSuffix(name, ".txt")
}
