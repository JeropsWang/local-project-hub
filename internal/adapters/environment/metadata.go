package environment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func readSmallRegularFile(file string, limit int64) ([]byte, error) {
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, problem.New("METADATA_FILE_UNSUPPORTED", "")
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, problem.New("METADATA_FILE_LIMIT", "")
	}
	return data, nil
}

// StaticInspector inspects declared file presence/hashes, never package lifecycle scripts.
// Its fingerprint covers declared inputs and is not proof that an entire Git tree is clean.
type StaticInspector struct{}

// Inspect uses rooted reads and refuses secret-like configuration content during detection.
func (StaticInspector) Inspect(ctx context.Context, req ports.InspectionRequest) (ports.SourceInspection, error) {
	result := ports.SourceInspection{Items: []check.Item{}}
	hash := sha256.New()
	source, _ := json.Marshal(req.Document.Source)
	_, _ = hash.Write(source)
	ids := make([]string, 0, len(req.Document.Files))
	for id := range req.Document.Files {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	total := int64(0)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		file := req.Document.Files[id]
		if !file.Required {
			continue
		}
		item := check.Item{RequirementID: "file:" + id, Status: check.Satisfied, Reasons: []check.Reason{}, Evidence: []check.Evidence{}, Repairs: []check.RepairStep{}}
		var root, relative string
		switch file.Availability {
		case "generated":
			item.Status = check.Unknown
			item.Reasons = append(item.Reasons, check.Reason{Code: "FILE_REQUIRES_GENERATION", Subject: id})
		case "included":
			root = req.WorkspaceRoot
			relative = filepath.ToSlash(filepath.Join(filesystem.RevisionPath(req.Document.ProjectID, req.Revision), file.BundlePath))
		case "external":
			if file.Path == nil {
				return result, problem.New("FILE_PATH_INVALID", id)
			}
			binding, ok := req.Bindings[file.Path.Binding]
			if !ok || binding.Kind != "path" {
				item.Status = check.BindingRequired
				item.Reasons = append(item.Reasons, check.Reason{Code: "FILE_BINDING_REQUIRED", Subject: id})
			} else {
				root = binding.LocalLocator
				relative = file.Path.RelativePath
			}
		}
		if root != "" {
			if !filepath.IsAbs(root) || strings.HasPrefix(root, `\\`) {
				item.Status = check.Unknown
				item.Reasons = append(item.Reasons, check.Reason{Code: "FILE_ROOT_UNSUPPORTED", Subject: id})
			} else {
				anchor, err := os.OpenRoot(root)
				if err != nil {
					item.Status = check.Missing
					item.Reasons = append(item.Reasons, check.Reason{Code: "FILE_ROOT_MISSING", Subject: id})
				} else {
					err = inspectFile(ctx, anchor, relative, file, &item, hash, &total)
					anchor.Close()
					if err != nil {
						return result, err
					}
				}
			}
		}
		result.Items = append(result.Items, item)
	}
	result.Fingerprint = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

func inspectFile(ctx context.Context, root *os.Root, relative string, file project.File, item *check.Item, fingerprint io.Writer, total *int64) error {
	if err := project.ValidateRelativePath(relative); err != nil {
		return err
	}
	// Reject links even when their targets would stay inside the root.
	current := ""
	for _, part := range strings.Split(relative, "/") {
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		info, err := root.Lstat(current)
		if err != nil {
			item.Status = check.Missing
			item.Reasons = append(item.Reasons, check.Reason{Code: "FILE_MISSING"})
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			item.Status = check.Incompatible
			item.Reasons = append(item.Reasons, check.Reason{Code: "FILE_LINK_UNSUPPORTED"})
			return nil
		}
	}
	info, err := root.Lstat(relative)
	if err != nil || !info.Mode().IsRegular() {
		item.Status = check.Incompatible
		item.Reasons = append(item.Reasons, check.Reason{Code: "FILE_NOT_REGULAR"})
		return nil
	}
	base := strings.ToLower(filepath.Base(relative))
	// Presence checks are enough for secret-bearing config. Do not load real .env values.
	if file.Role == "config" || strings.HasPrefix(base, ".env") || strings.Contains(base, "secret") || strings.Contains(base, "credential") {
		_, _ = io.WriteString(fingerprint, relative+":presence;")
		if file.ExpectedSHA256 != "" {
			item.Status = check.Unknown
			item.Reasons = append(item.Reasons, check.Reason{Code: "CONFIG_HASH_NOT_VERIFIED", Message: "Presence was checked without loading potentially secret configuration."})
		}
		return nil
	}
	if info.Size() > 16*1024*1024 || *total+info.Size() > 128*1024*1024 {
		item.Status = check.Unknown
		item.Reasons = append(item.Reasons, check.Reason{Code: "FILE_HASH_BUDGET"})
		return nil
	}
	f, err := root.Open(relative)
	if err != nil {
		item.Status = check.Missing
		return nil
	}
	defer f.Close()
	digest := sha256.New()
	count, err := io.Copy(digest, io.LimitReader(f, 16*1024*1024+1))
	if err != nil {
		return err
	}
	if count > 16*1024*1024 {
		item.Status = check.Unknown
		item.Reasons = append(item.Reasons, check.Reason{Code: "FILE_HASH_BUDGET"})
		return nil
	}
	*total += count
	if err := ctx.Err(); err != nil {
		return err
	}
	sum := hex.EncodeToString(digest.Sum(nil))
	_, _ = io.WriteString(fingerprint, relative+":"+sum+";")
	if file.ExpectedSHA256 != "" && file.ExpectedSHA256 != sum {
		item.Status = check.Incompatible
		item.Reasons = append(item.Reasons, check.Reason{Code: "FILE_HASH_MISMATCH"})
	}
	return nil
}
