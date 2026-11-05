package environment

import (
	"debug/pe"
	"encoding/json"
	"regexp"
	"runtime"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
)

// toolArgs contains no project-provided source. No npm lifecycle or setup is executed.
func toolArgs(tool string) (string, []string, error) {
	switch tool {
	case "node":
		return "node.exe", []string{"-p", `JSON.stringify({version:process.versions.node,architecture:process.arch})`}, nil
	case "go":
		return "go.exe", []string{"version"}, nil
	case "python":
		// -I ignores environment options, so bytecode suppression must be an
		// explicit flag. Otherwise a version query can rewrite bundled .pyc files.
		return "python.exe", []string{"-I", "-B", "-S", "-c", `import json,sys,sysconfig,struct; print(json.dumps({"version":".".join(map(str,sys.version_info[:3])),"architecture":sysconfig.get_platform().split("-")[-1],"abi":sys.implementation.cache_tag,"executable":sys.executable}))`}, nil
	case "java":
		return "java.exe", []string{"-XshowSettings:properties", "-version"}, nil
	case "docker":
		return "docker.exe", []string{"version", "--format", "{{json .}}"}, nil
	case "compose":
		return "docker.exe", []string{"compose", "version", "--format", "json"}, nil
	case "powershell", "pwsh":
		return tool + ".exe", []string{"-NoProfile", "-NonInteractive", "-Command", `@{version=$PSVersionTable.PSVersion.ToString();architecture=if([IntPtr]::Size -eq 8){'amd64'}else{'386'}} | ConvertTo-Json -Compress`}, nil
	case "postgres":
		return "postgres.exe", []string{"--version"}, nil
	case "mysql":
		return "mysqld.exe", []string{"--version"}, nil
	case "redis":
		return "redis-server.exe", []string{"--version"}, nil
	case "npm":
		// npm is read from its adjacent package manifest, not a command shim.
		return "node.exe", nil, nil
	case "external":
		return "", nil, nil
	default:
		return "", nil, problem.New("PROBE_TOOL_UNSUPPORTED", "")
	}
}

var versionText = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?(?:[-+][0-9A-Za-z.-]+)?`)

func normalizeArch(value string) string {
	switch strings.ToLower(value) {
	case "x64", "x86_64", "amd64", "win-amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "x86", "i386", "386", "win32":
		return "386"
	}
	return ""
}

type versionJSON struct {
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	ABI          string `json:"abi"`
}

func parseVersion(tool, text, locator string) (version, architecture, abi string, capabilities []string) {
	var doc versionJSON
	if json.Unmarshal([]byte(text), &doc) == nil && doc.Version != "" {
		return strings.TrimPrefix(doc.Version, "v"), normalizeArch(doc.Architecture), doc.ABI, []string{"version-observation"}
	}
	if tool == "docker" {
		var data struct {
			Server *struct{ Version, Arch, Os string }
		}
		if json.Unmarshal([]byte(text), &data) == nil && data.Server != nil {
			capabilities = []string{"engine-observation"}
			if data.Server.Os == "linux" {
				capabilities = append(capabilities, "linux-containers")
			}
			return data.Server.Version, normalizeArch(data.Server.Arch), "", capabilities
		}
		return "", "", "", nil
	}
	version = versionText.FindString(text)
	if tool == "go" {
		fields := strings.Fields(strings.TrimSpace(text))
		if len(fields) >= 4 && strings.HasPrefix(fields[2], "go") {
			version = strings.TrimPrefix(fields[2], "go")
			_, arch, _ := strings.Cut(fields[3], "/")
			architecture = normalizeArch(arch)
		} else {
			version = ""
		}
	}
	if tool == "java" {
		// Settings include class.version (e.g. 55.0 for Java 11), which is not
		// the JVM version. Prefer the exact property and only then a version banner.
		version = ""
		for _, line := range strings.Split(text, "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(key) == "java.version" {
				version = strings.TrimSpace(value)
			}
			if ok && strings.TrimSpace(key) == "os.arch" {
				architecture = normalizeArch(strings.TrimSpace(value))
			}
		}
		if version == "" {
			banner := regexp.MustCompile(`(?m)^(?:openjdk|java)(?: version)?\s+"?([0-9][0-9A-Za-z._+\-]*)`).FindStringSubmatch(text)
			if banner != nil {
				version = banner[1]
			}
		}
	}
	if architecture == "" {
		if file, err := pe.Open(locator); err == nil {
			switch file.Machine {
			case pe.IMAGE_FILE_MACHINE_AMD64:
				architecture = "amd64"
			case pe.IMAGE_FILE_MACHINE_ARM64:
				architecture = "arm64"
			case pe.IMAGE_FILE_MACHINE_I386:
				architecture = "386"
			}
			file.Close()
		} else if runtime.GOOS != "windows" {
			architecture = ""
		}
	}
	return version, architecture, "", []string{"version-observation"}
}
