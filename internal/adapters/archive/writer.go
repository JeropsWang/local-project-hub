package archive

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/identity"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

type Writer struct{}

// Inspect validates the declared closure before the user submits an export.
func (Writer) Inspect(input bundle.ExportInput) (bundle.Closure, error) {
	doc, err := contract.DecodeProject(input.Project)
	if err != nil {
		return bundle.Closure{}, err
	}
	closure, err := bundle.Collect(doc, func(name string) ([]byte, error) {
		data, ok := input.Files[name]
		if !ok {
			return nil, problem.New("BUNDLE_FILE_MISSING", name)
		}
		return data, nil
	})
	if err != nil {
		return bundle.Closure{}, err
	}
	if len(input.Files) != len(closure.Files) {
		return bundle.Closure{}, problem.New("BUNDLE_INVENTORY_MISMATCH", "")
	}
	if err = validateComposeClosure(doc, closure.Files); err != nil {
		return bundle.Closure{}, err
	}
	return closure, nil
}

// Write validates and publishes a complete package with a same-directory hard
// link. Unlike a replacing rename, the link cannot overwrite a racing writer.
func (writerAdapter Writer) Write(ctx context.Context, input bundle.ExportInput) (bundle.ExportResult, error) {
	var result bundle.ExportResult
	closure, err := writerAdapter.Inspect(input)
	if err != nil {
		return result, err
	}
	var declaration struct {
		ProjectID string `json:"projectId"`
	}
	if err = json.Unmarshal(input.Project, &declaration); err != nil {
		return result, err
	}
	closure.Files[bundle.ProjectFile] = input.Project
	names := make([]string, 0, len(closure.Files))
	for name := range closure.Files {
		names = append(names, name)
	}
	slices.Sort(names)
	manifest := bundle.Manifest{Format: bundle.Format, BundleVersion: bundle.Version, MinManagerVersion: "0.1.0",
		ProjectID: declaration.ProjectID, ProjectFile: bundle.ProjectFile, ExternalBindingKeys: closure.ExternalBindingKeys,
		CreatedAt: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)}
	for _, name := range names {
		data := closure.Files[name]
		manifest.Files = append(manifest.Files, bundle.Entry{Path: name, Size: len(data), SHA256: bundle.Digest(data)})
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return result, err
	}
	if !filepath.IsAbs(input.Destination) {
		return result, problem.New("EXPORT_DESTINATION_INVALID", "")
	}
	target := filepath.Base(input.Destination)
	if err = project.ValidateRelativePath(target); err != nil || target == "." {
		return result, problem.New("EXPORT_DESTINATION_INVALID", "")
	}
	root, err := filesystem.OpenLocalRoot(filepath.Dir(input.Destination))
	if err != nil {
		return result, err
	}
	defer root.Close()
	id, err := identity.NewUUID()
	if err != nil {
		return result, err
	}
	stage := ".lph-export-" + id + ".tmp"
	file, err := root.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return result, err
	}
	defer root.Remove(stage)
	defer file.Close()
	writer := zip.NewWriter(file)
	for _, name := range append(names, bundle.ManifestFile) {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		content := closure.Files[name]
		if name == bundle.ManifestFile {
			content = data
		}
		entry, err := writer.Create(name)
		if err != nil {
			return result, err
		}
		if _, err = entry.Write(content); err != nil {
			return result, err
		}
	}
	if err = writer.Close(); err != nil {
		return result, err
	}
	if err = file.Sync(); err != nil {
		return result, err
	}
	info, err := file.Stat()
	if err != nil {
		return result, err
	}
	if _, _, err = validateZIP(ctx, file, info.Size()); err != nil {
		return result, err
	}
	if err = file.Close(); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = root.Link(stage, target); err != nil {
		if _, statErr := root.Lstat(target); statErr == nil {
			return result, problem.New("EXPORT_DESTINATION_EXISTS", "")
		}
		return result, err
	}
	return bundle.ExportResult{Manifest: manifest, Warnings: closure.Warnings}, nil
}
