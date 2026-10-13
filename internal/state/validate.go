package state

import (
	"fmt"
	"path/filepath"
	"workcopy/internal/fsops"
	"workcopy/internal/model"
)

func validateVersion(version model.FileVersion) error {
	switch version.Kind {
	case "", model.Missing:
		if version.Mode != 0 || version.Size != 0 || version.Hash != "" || version.ObjectID != "" {
			return fmt.Errorf("missing file has content metadata")
		}
		return nil
	case model.Regular:
		if version.Mode != 0644 && version.Mode != 0755 {
			return fmt.Errorf("invalid regular file mode")
		}
	case model.Symlink:
		if version.Mode != 0120000 {
			return fmt.Errorf("invalid symlink mode")
		}
	default:
		return fmt.Errorf("unsupported file kind %q", version.Kind)
	}
	if !digest.MatchString(version.Hash) || version.Size < 0 || version.Size > model.MaxFileBytes {
		return fmt.Errorf("invalid file size or content hash")
	}
	if version.ObjectID != "" && !validHex(version.ObjectID, 40) && !validHex(version.ObjectID, 64) {
		return fmt.Errorf("invalid Git object ID")
	}
	return nil
}
func validHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9') && !(ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}
func validateSnapshot(snapshot model.Snapshot) error {
	if snapshot.Manifest.SchemaVersion != model.SchemaVersion || !model.ValidID(snapshot.Manifest.ID) || snapshot.Manifest.Generation == 0 {
		return fmt.Errorf("invalid state schema, identity or generation")
	}
	for name, version := range snapshot.Baseline.Files {
		if err := fsops.ValidateSourcePath(name); err != nil {
			return err
		}
		if err := validateVersion(version); err != nil {
			return fmt.Errorf("baseline %s: %w", name, err)
		}
	}
	conflicts := map[string]bool{}
	for _, conflict := range snapshot.Conflicts {
		if !model.ValidID(conflict.ID) || conflicts[conflict.Path] {
			return fmt.Errorf("invalid or duplicate conflict")
		}
		conflicts[conflict.Path] = true
		if err := fsops.ValidateSourcePath(conflict.Path); err != nil {
			return err
		}
		for _, version := range []model.FileVersion{conflict.Base, conflict.Local, conflict.Upstream} {
			if err := validateVersion(version); err != nil {
				return err
			}
		}
	}
	mappings := map[string]bool{}
	for _, mapping := range snapshot.Manifest.Mappings {
		if err := fsops.ValidateSourcePath(mapping.Path); err != nil {
			return err
		}
		if mappings[mapping.Path] || !filepath.IsAbs(mapping.Target) {
			return fmt.Errorf("invalid mapping record")
		}
		mappings[mapping.Path] = true
	}
	reviews := map[string]bool{}
	for _, review := range snapshot.Manifest.Reviews {
		if !model.ValidID(review.ID) || reviews[review.ID] {
			return fmt.Errorf("invalid or duplicate review identity")
		}
		reviews[review.ID] = true
		if review.State != "preparing" && review.State != "prepared" && review.State != "committing" && review.State != "committed" {
			return fmt.Errorf("invalid review phase")
		}
		for name, version := range review.Files {
			if err := fsops.ValidateSourcePath(name); err != nil {
				return err
			}
			if err := validateVersion(version); err != nil {
				return err
			}
		}
	}
	return nil
}
