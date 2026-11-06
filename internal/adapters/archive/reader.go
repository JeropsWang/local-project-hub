// Package archive reads and writes bounded configuration ZIP packages.
package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"path"
	"path/filepath"
	"slices"

	"github.com/Masterminds/semver/v3"

	"github.com/JeropsWang/local-project-hub/internal/adapters/contract"
	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
)

type Reader struct{}

// ReadArchive validates a bounded byte snapshot without extracting files.
func (Reader) ReadArchive(ctx context.Context, source string) (bundle.ImportPayload, error) {
	var result bundle.ImportPayload
	if !filepath.IsAbs(source) {
		return result, problem.New("IMPORT_SOURCE_INVALID", "")
	}
	root, err := filesystem.OpenLocalRoot(filepath.Dir(source))
	if err != nil {
		return result, err
	}
	defer root.Close()
	name := filepath.Base(source)
	if project.ValidateRelativePath(name) != nil || name == "." {
		return result, problem.New("IMPORT_SOURCE_INVALID", "")
	}
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > bundle.MaxArchive {
		return result, problem.New("IMPORT_SOURCE_INVALID", "")
	}
	file, err := root.Open(name)
	if err != nil {
		return result, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, bundle.MaxArchive+1))
	if err != nil {
		return result, err
	}
	manifest, files, err := validateZIP(ctx, bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return result, err
	}
	doc, err := contract.DecodeProject(files[bundle.ProjectFile])
	if err != nil {
		return result, err
	}
	result.Inspection = bundle.Inspection{
		Identity:         bundle.Digest(raw),
		Document:         doc,
		RequiredBindings: manifest.ExternalBindingKeys,
	}
	for _, file := range doc.Files {
		if file.Role == "script" {
			result.Inspection.Warnings = []string{"SCRIPT_DYNAMIC_REFERENCES_UNVERIFIED"}
			break
		}
	}
	result.Project = files[bundle.ProjectFile]
	delete(files, bundle.ProjectFile)
	result.Files = files
	return result, nil
}

// validateZIP verifies the manifest against actual bytes and the project file
// closure. It writes nothing, so validation cannot escape a staging directory.
func validateZIP(ctx context.Context, source io.ReaderAt, size int64) (bundle.Manifest, map[string][]byte, error) {
	fail := func(code, name string) (bundle.Manifest, map[string][]byte, error) {
		return bundle.Manifest{}, nil, problem.New(code, name)
	}
	if err := checkDirectory(source, size); err != nil {
		return bundle.Manifest{}, nil, err
	}
	reader, err := zip.NewReader(source, size)
	if err != nil {
		return fail("BUNDLE_ZIP_INVALID", "")
	}
	if len(reader.File) > bundle.MaxFiles+1 {
		return fail("BUNDLE_LIMIT", "")
	}
	files := map[string][]byte{}
	aliases := map[string]bool{}
	total := 0
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return bundle.Manifest{}, nil, err
		}
		name := file.Name
		key := bundle.PathKey(name)
		if aliases[key] || !file.Mode().IsRegular() || file.Flags&1 != 0 || file.NonUTF8 {
			return fail("BUNDLE_ZIP_ENTRY_INVALID", name)
		}
		aliases[key] = true
		limit := bundle.MaxFile
		switch name {
		case bundle.ManifestFile:
			limit = bundle.MaxManifest
		case bundle.ProjectFile:
		default:
			if err := bundle.ValidateFilePath(name); err != nil {
				return bundle.Manifest{}, nil, err
			}
		}
		limit = min(limit, bundle.MaxExpanded-total)
		if file.UncompressedSize64 > uint64(limit) {
			return fail("BUNDLE_LIMIT", name)
		}
		stream, err := file.Open()
		if err != nil {
			return fail("BUNDLE_ZIP_ENTRY_INVALID", name)
		}
		data, err := io.ReadAll(io.LimitReader(stream, int64(limit)+1))
		stream.Close()
		if len(data) > limit {
			return fail("BUNDLE_LIMIT", name)
		}
		if err != nil || uint64(len(data)) != file.UncompressedSize64 {
			return fail("BUNDLE_ZIP_ENTRY_INVALID", name)
		}
		total += len(data)
		files[name] = data
	}
	manifest, err := contract.DecodeManifest(files[bundle.ManifestFile])
	if err != nil {
		return bundle.Manifest{}, nil, err
	}
	minimum, err := semver.StrictNewVersion(manifest.MinManagerVersion)
	if err != nil || minimum.GreaterThan(semver.New(0, 1, 0, "", "")) {
		return fail("BUNDLE_MANAGER_VERSION_UNSUPPORTED", "")
	}
	for name := range files {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if aliases[bundle.PathKey(parent)] {
				return fail("BUNDLE_PATH_CONFLICT", name)
			}
		}
	}
	delete(files, bundle.ManifestFile)
	if len(files) != len(manifest.Files) {
		return fail("BUNDLE_INVENTORY_MISMATCH", "")
	}
	listed := map[string]bool{}
	for _, entry := range manifest.Files {
		data, ok := files[entry.Path]
		if !ok || listed[entry.Path] || len(data) != entry.Size || bundle.Digest(data) != entry.SHA256 {
			return fail("BUNDLE_INVENTORY_MISMATCH", entry.Path)
		}
		listed[entry.Path] = true
	}
	doc, err := contract.DecodeProject(files[bundle.ProjectFile])
	if err != nil {
		return bundle.Manifest{}, nil, err
	}
	closure, err := bundle.Collect(doc, func(name string) ([]byte, error) {
		data, ok := files[name]
		if !ok {
			return nil, problem.New("BUNDLE_FILE_MISSING", name)
		}
		return data, nil
	})
	if err != nil {
		return bundle.Manifest{}, nil, err
	}
	slices.Sort(manifest.ExternalBindingKeys)
	if doc.ProjectID != manifest.ProjectID || len(files) != len(closure.Files)+1 ||
		!slices.Equal(closure.ExternalBindingKeys, manifest.ExternalBindingKeys) {
		return fail("BUNDLE_INVENTORY_MISMATCH", "")
	}
	if err := validateComposeClosure(doc, closure.Files); err != nil {
		return bundle.Manifest{}, nil, err
	}
	return manifest, files, nil
}
