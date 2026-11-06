// Package state owns atomic snapshots, writer locks and replayable mutations.
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"runtime"
	"strings"

	"workcopy/internal/eol"
	"workcopy/internal/fsops"
	"workcopy/internal/model"
)

const ManifestPath = ".workcopy/manifest.json"
const JournalPath = ".workcopy/journal.json"

var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ObjectSource supplies file contents that already exist in Git. A transaction
// that writes such content records the Git object instead of staging a second
// copy of every file in the lane's object store, which on a slow filesystem
// was the largest single cost of creating a lane. Implementations must be safe
// for concurrent use.
type ObjectSource interface {
	Read(objectID string) ([]byte, error)
}

type Store struct {
	Root *fsops.Root
	// Objects is optional. Without it every written byte is staged in the store.
	Objects ObjectSource
}

func New(root *fsops.Root) *Store { return &Store{Root: root} }
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
	snapshot.Manifest.RespectExecutableBits = s.RespectExecutableBits(snapshot.Manifest.RespectExecutableBits)
	return snapshot, nil
}

// modeProbePath is a file Workcopy creates once with mode 0600 and never
// rewrites, so its reported permissions reveal whether the filesystem lies.
const modeProbePath = ".workcopy/lock"

// RespectExecutableBits narrows a lane's declared setting to what the lane's
// filesystem can honour. A lane created before this check existed on a
// filesystem that reports every file as executable (WC-001) heals on its next
// read instead of showing every file as modified.
func (s *Store) RespectExecutableBits(declared bool) bool {
	if !declared || runtime.GOOS == "windows" {
		return false
	}
	faithful, known := s.Root.ExecutableBitsFaithful(modeProbePath)
	return faithful || !known
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
	stale := []string{}
	if err := s.Root.Walk(func(name string, entry fs.DirEntry, err error) error {
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
			stale = append(stale, name)
		}
		return nil
	}); err != nil {
		return err
	}
	// Unlinking thousands of staged objects is latency-bound on Windows and 9p.
	return fsops.Parallel(context.Background(), len(stale), func(index int) error {
		return s.Root.Remove(stale[index])
	})
}
func ReadVersion(root *fsops.Root, name string, baseline model.FileVersion, respectMode bool) (model.FileVersion, []byte, error) {
	version, body, _, err := readVersion(root, name, baseline, respectMode)
	return version, body, err
}

// readVersion is ReadVersion that also reports whether the path was a
// directory, which reads as Missing. A caller about to replace the path can then
// skip a second stat for the directory check, which matters on filesystems
// where every stat is a network round trip.
func readVersion(root *fsops.Root, name string, baseline model.FileVersion, respectMode bool) (model.FileVersion, []byte, bool, error) {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return model.FileVersion{Kind: model.Missing}, nil, false, nil
	}
	if err != nil {
		return model.FileVersion{}, nil, false, err
	}
	if linkCandidate(info.Mode(), runtime.GOOS) {
		target, err := root.Readlink(name)
		body := []byte(canonicalLinkTarget(target, runtime.GOOS))
		return model.Version(model.Symlink, 0120000, body), body, false, err
	}
	if info.IsDir() {
		return model.FileVersion{Kind: model.Missing}, nil, true, nil
	}
	if !info.Mode().IsRegular() {
		return model.FileVersion{}, nil, false, model.Fail(3, "path-obstructed", "Expected a source file, found a directory or special file: "+name)
	}
	body, err := root.ReadRegular(name, info, model.MaxFileBytes)
	if err != nil {
		return model.FileVersion{}, nil, false, err
	}
	mode := uint32(0644)
	if respectMode && runtime.GOOS != "windows" && info.Mode().Perm()&0111 != 0 {
		mode = 0755
	} else if !respectMode && baseline.Kind == model.Regular {
		mode = baseline.Mode
	}
	// A text file is compared, hashed and merged in its stored (LF) form so a
	// tool that rewrites line endings cannot create a change or a conflict.
	// For a file with no baseline, baseline.Eol may carry an unresolved rule
	// (for example "auto-crlf") that depends on the content just read.
	token := eol.ForLocal(baseline.Eol, body)
	if token != eol.Raw {
		body = eol.Normalize(body)
	}
	version := model.Version(model.Regular, mode, body)
	version.Eol = token
	return version, body, false, nil
}

// canonicalLinkTarget returns a link target in the form Git stores it.
//
// Windows only resolves a relative symlink target that uses backslashes, so
// Go converts the slashes of a target when it creates the link (os.Symlink, and
// os.Root.Symlink in the Go 1.27 toolchain this project pins; the CI job on Go
// 1.25 did not show the mismatch) and returns the backslash form from Readlink.
// Git, the baseline and the review commit all hold the slash form. Without this
// a link Workcopy just wrote would read back as an edit. Source links are
// validated to be relative and never contain a backslash, so on Windows every
// backslash in a source link is a separator; elsewhere a backslash is an
// ordinary character and the target is returned untouched.
//
// Only relative targets are converted. A shared-dependency mapping is an
// absolute directory link (C:\cache\deps, \\server\share, \??\C:\...) that
// Workcopy records and compares in the form Windows reports, so absolute
// targets are returned exactly as read.
func canonicalLinkTarget(target, platform string) string {
	if platform != "windows" || target == "" || target[0] == '\\' || target[0] == '/' || (len(target) >= 2 && target[1] == ':') {
		return target
	}
	return strings.ReplaceAll(target, `\`, "/")
}

func linkCandidate(mode os.FileMode, platform string) bool {
	// Windows mount points are ModeIrregular, not ModeSymlink. Readlink still
	// validates that the reparse tag is a symlink or junction before use.
	return mode&os.ModeSymlink != 0 || platform == "windows" && mode&os.ModeIrregular != 0
}
