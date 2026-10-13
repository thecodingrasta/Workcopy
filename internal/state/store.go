// Package state owns atomic snapshots, writer locks and replayable mutations.
package state

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"runtime"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
)

const ManifestPath = ".workcopy/manifest.json"
const JournalPath = ".workcopy/journal.json"

var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Store struct{ Root *fsops.Root }

func New(root *fsops.Root) *Store { return &Store{root} }
func (s *Store) Load() (model.Snapshot, error) {
	var snapshot model.Snapshot
	body, err := s.Root.ReadFileLimit(ManifestPath, model.MaxMetadataBytes)
	if err != nil {
		return snapshot, err
	}
	if err = model.DecodeStrict(body, &snapshot); err != nil {
		return snapshot, err
	}
	if snapshot.Manifest.SchemaVersion != model.SchemaVersion {
		return snapshot, model.Fail(3, "unsupported-state-version", "State schema is not supported")
	}
	if err = validateSnapshot(snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}
func (s *Store) Save(snapshot model.Snapshot) error {
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	body, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if int64(len(body)) > model.MaxMetadataBytes {
		return fmt.Errorf("state exceeds metadata limit")
	}
	return s.Root.WriteAtomic(ManifestPath, append(body, '\n'), 0600)
}
func (s *Store) Lock() (*Lock, error) { return Acquire(s.Root, ".workcopy/lock") }
func (s *Store) PutBlob(body []byte) (string, error) {
	hash := model.Hash(body)
	name := ".workcopy/objects/" + hash
	existing, err := s.Root.ReadFile(name)
	if err == nil {
		if model.Hash(existing) != hash {
			return "", fmt.Errorf("corrupt content object")
		}
		return hash, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	return hash, s.Root.WriteAtomic(name, body, 0600)
}
func (s *Store) Blob(hash string) ([]byte, error) {
	if !digest.MatchString(hash) {
		return nil, fmt.Errorf("invalid content hash")
	}
	body, err := s.Root.ReadFile(".workcopy/objects/" + hash)
	if err != nil {
		return nil, err
	}
	if model.Hash(body) != hash {
		return nil, fmt.Errorf("content object hash mismatch")
	}
	return body, nil
}
func (s *Store) collect(snapshot model.Snapshot) error {
	keep := map[string]bool{}
	for _, review := range snapshot.Manifest.Reviews {
		if review.State == "preparing" {
			for _, version := range review.Files {
				if version.Exists() && version.ObjectID == "" {
					keep[version.Hash] = true
				}
			}
		}
	}
	for _, c := range snapshot.Conflicts {
		for _, v := range []model.FileVersion{c.Base, c.Local, c.Upstream} {
			if v.Exists() && v.ObjectID == "" {
				keep[v.Hash] = true
			}
		}
	}
	return s.Root.Walk(func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name != "." && name != ".workcopy" && name != ".workcopy/objects" {
				return fs.SkipDir
			}
			return nil
		}
		if path.Dir(name) != ".workcopy/objects" {
			return nil
		}
		base := path.Base(name)
		if !digest.MatchString(base) || !entry.Type().IsRegular() {
			return fmt.Errorf("unrecognized object-store entry: %s", name)
		}
		if !keep[base] {
			return s.Root.Remove(name)
		}
		return nil
	})
}
func ReadVersion(root *fsops.Root, name string, baseline model.FileVersion, respectMode bool) (model.FileVersion, []byte, error) {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return model.FileVersion{Kind: model.Missing}, nil, nil
	}
	if err != nil {
		return model.FileVersion{}, nil, err
	}
	if linkCandidate(info.Mode(), runtime.GOOS) {
		target, err := root.Readlink(name)
		body := []byte(target)
		return model.Version(model.Symlink, 0120000, body), body, err
	}
	if info.IsDir() {
		return model.FileVersion{Kind: model.Missing}, nil, nil
	}
	if !info.Mode().IsRegular() {
		return model.FileVersion{}, nil, model.Fail(3, "path-obstructed", "Expected a source file, found a directory or special file: "+name)
	}
	body, err := root.ReadFile(name)
	if err != nil {
		return model.FileVersion{}, nil, err
	}
	mode := uint32(0644)
	if respectMode && runtime.GOOS != "windows" && info.Mode().Perm()&0111 != 0 {
		mode = 0755
	} else if !respectMode && baseline.Kind == model.Regular {
		mode = baseline.Mode
	}
	return model.Version(model.Regular, mode, body), body, nil
}

func linkCandidate(mode os.FileMode, platform string) bool {
	// Windows mount points are ModeIrregular, not ModeSymlink. Readlink still
	// validates that the reparse tag is a symlink or junction before use.
	return mode&os.ModeSymlink != 0 || platform == "windows" && mode&os.ModeIrregular != 0
}
