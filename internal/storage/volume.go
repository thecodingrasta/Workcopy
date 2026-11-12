package storage

import (
	"os"
	"path/filepath"
)

// Support is a three-valued answer. Unknown is used when only an attempt on the
// real volume could decide, and must not be read as a refusal.
type Support string

const (
	Yes     Support = "yes"
	No      Support = "no"
	Unknown Support = "unknown"
)

// Volume describes the filesystem a path lives on.
type Volume struct {
	// Path is the existing directory that was inspected: the requested path, or
	// its nearest existing ancestor.
	Path string `json:"path"`
	// ID is opaque. Two paths are on one volume exactly when their IDs are equal.
	ID         string  `json:"id"`
	FileSystem string  `json:"fileSystem"`
	HardLinks  Support `json:"hardLinks"`
	// BlockClone says whether the volume can share data blocks between files.
	// CloneBasis names where that answer came from. Neither is proof that a clone
	// of a given file will succeed.
	BlockClone Support `json:"blockClone"`
	CloneBasis string  `json:"cloneBasis,omitempty"`
	FreeBytes  uint64  `json:"freeBytes"`
	TotalBytes uint64  `json:"totalBytes"`
}

// SameVolume reports whether both describe one volume. Hard links and clones
// only work within a volume.
func SameVolume(a, b Volume) bool { return a.ID != "" && a.ID == b.ID }

// existingAncestor returns path, or the nearest parent that exists.
func existingAncestor(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for {
		if _, err = os.Stat(absolute); err == nil {
			return absolute, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			return "", err
		}
		absolute = parent
	}
}
