package environment

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

var sourceMarkers = []string{
	"package.json", "go.mod", "pyproject.toml", "requirements.txt", "pom.xml",
	"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml",
}

// InspectSource reports regular files at fixed paths without opening their
// contents, executing the project, or creating any files in its root.
func InspectSource(ctx context.Context, root string) (project.SourceDiscovery, error) {
	if err := ctx.Err(); err != nil {
		return project.SourceDiscovery{}, err
	}
	anchor, err := filesystem.OpenLocalRoot(root)
	if err != nil {
		return project.SourceDiscovery{}, err
	}
	defer anchor.Close()
	result := project.SourceDiscovery{Name: filepath.Base(filepath.Clean(root)), Markers: []string{}}
	for _, name := range sourceMarkers {
		if err := ctx.Err(); err != nil {
			return project.SourceDiscovery{}, err
		}
		info, err := anchor.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return project.SourceDiscovery{}, err
		}
		if info.Mode().IsRegular() {
			result.Markers = append(result.Markers, name)
		}
	}
	return result, nil
}
