package fsops

import (
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"workcopy/internal/model"
)

// TempPrefix starts the name of every file Workcopy writes before renaming it
// into place (atomic replacement) and of the symbolic link it makes the same
// way. A process killed between creating one and renaming it leaves it behind.
const TempPrefix = ".workcopy-tmp-"

// newTempID returns the random part of a temporary file name.
func newTempID() (string, error) { return model.ID() }

// IsTempArtifact reports whether a path's last element is exactly a name
// Workcopy generates for an atomic write: the prefix and 32 lower-case hex
// digits. A person's file that merely starts with the prefix is not one.
func IsTempArtifact(name string) bool {
	base := path.Base(strings.ReplaceAll(name, `\`, "/"))
	if len(base) != len(TempPrefix)+32 || !strings.HasPrefix(base, TempPrefix) {
		return false
	}
	for _, c := range base[len(TempPrefix):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// RemoveTempArtifacts deletes interrupted atomic-write leftovers directly
// inside each named directory (relative to the root, "." for the root itself)
// and returns the paths it removed. Only entries that are not directories and
// carry an exact generated name are touched; a missing directory is skipped.
func (r *Root) RemoveTempArtifacts(directories []string) ([]string, error) {
	unique := map[string]bool{}
	for _, directory := range directories {
		unique[directory] = true
	}
	ordered := make([]string, 0, len(unique))
	for directory := range unique {
		ordered = append(ordered, directory)
	}
	sort.Strings(ordered)
	removed := []string{}
	for _, directory := range ordered {
		if directory != "." {
			if err := r.guard(directory, true); err != nil {
				return removed, err
			}
		}
		entries, err := fs.ReadDir(r.backend.filesystem(), directory)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return removed, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !IsTempArtifact(entry.Name()) {
				continue
			}
			name := path.Join(directory, entry.Name())
			if directory == "." {
				name = entry.Name()
			}
			if err = r.Remove(name); err != nil && !os.IsNotExist(err) {
				return removed, err
			}
			removed = append(removed, name)
		}
	}
	return removed, nil
}
