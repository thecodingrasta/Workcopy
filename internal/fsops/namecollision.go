package fsops

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path"
	"sort"
	"strings"
)

// NameCollision names two tracked paths that the volume stores as one entry.
// First is empty when the earlier spelling could not be identified.
type NameCollision struct{ First, Second string }

const (
	// partnerSearchLimit bounds the work spent naming the earlier spelling, which
	// only matters for the error message.
	partnerSearchLimit = 500
	nameProbePrefix    = ".workcopy-name-probe-"
)

func hasNonASCII(name string) bool {
	for index := 0; index < len(name); index++ {
		if name[index] >= 0x80 {
			return true
		}
	}
	return false
}

// FindNameCollision reports the first pair of non-ASCII paths in names that the
// volume under the root treats as the same entry, or nil.
//
// Case-only collisions are decided before anything is written. What a volume
// treats as the same name is not knowable from the names alone: macOS stores
// "caf\u00e9" and "cafe\u0301" as one file, Linux and Windows as two. Rather than
// ship Unicode tables that would still miss a volume's own rules, this asks the
// volume: it replays the layout of the non-ASCII paths as empty directories in a
// private scratch directory and notes where creating a path finds an entry that
// a different spelling already made. The scratch directory is removed before
// returning. It is skipped (one directory and one lookup) when the volume keeps
// canonically different spellings apart, which Linux and Windows do.
func (r *Root) FindNameCollision(names []string) (*NameCollision, error) {
	return r.findNameCollision(names, false)
}

// findNameCollision is FindNameCollision with the quick normalization check
// optionally disabled, so tests can exercise the layout replay on volumes whose
// only equivalence is case folding.
func (r *Root) findNameCollision(names []string, replayAlways bool) (found *NameCollision, err error) {
	candidates := []string{}
	for _, name := range names {
		if hasNonASCII(name) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) < 2 {
		return nil, nil
	}
	sort.Strings(candidates)

	var suffix [6]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		return nil, err
	}
	base := nameProbePrefix + hex.EncodeToString(suffix[:])
	if err = r.MkdirAll(base, 0700); err != nil {
		return nil, err
	}
	created := []string{} // paths below base, parents before children
	defer func() {
		for index := len(created) - 1; index >= 0; index-- {
			if removeErr := r.Remove(base + "/" + created[index]); removeErr != nil && err == nil {
				err = removeErr
			}
		}
		if removeErr := r.Remove(base); removeErr != nil && err == nil {
			err = removeErr
		}
	}()
	mkdir := func(relative string) error {
		if makeErr := r.MkdirAll(base+"/"+relative, 0700); makeErr != nil {
			return makeErr
		}
		created = append(created, relative)
		return nil
	}

	if !replayAlways {
		if err = mkdir("normalization"); err != nil {
			return nil, err
		}
		if err = mkdir("normalization/caf\u00e9"); err != nil {
			return nil, err
		}
		if _, statErr := r.Lstat(base + "/normalization/cafe\u0301"); statErr != nil {
			if os.IsNotExist(statErr) {
				return nil, nil // distinct spellings stay distinct files here
			}
			return nil, statErr
		}
	}

	if err = mkdir("layout"); err != nil {
		return nil, err
	}
	made := map[string]bool{}
	children := map[string][]string{} // parent path below layout -> child names created in it
	for _, name := range candidates {
		prefix, parent := "layout", ""
		for _, part := range strings.Split(name, "/") {
			prefix += "/" + part
			if made[prefix] {
				parent += "/" + part
				continue
			}
			if _, statErr := r.Lstat(base + "/" + prefix); statErr == nil {
				first := r.earlierSpelling(base, children[parent], part)
				if first != "" {
					first = strings.TrimPrefix(parent+"/"+first, "/")
				}
				return &NameCollision{First: first, Second: strings.TrimPrefix(parent+"/"+part, "/")}, nil
			} else if !os.IsNotExist(statErr) {
				return nil, statErr
			}
			if err = mkdir(prefix); err != nil {
				return nil, err
			}
			made[prefix] = true
			children[parent] = append(children[parent], part)
			parent += "/" + part
		}
	}
	return nil, nil
}

// earlierSpelling finds which sibling the volume equated with candidate by
// replaying each in a scratch directory. Only used to word the error.
func (r *Root) earlierSpelling(base string, siblings []string, candidate string) string {
	if len(siblings) > partnerSearchLimit {
		siblings = siblings[:partnerSearchLimit]
	}
	scratch := base + "/partner"
	if err := r.MkdirAll(scratch, 0700); err != nil {
		return ""
	}
	defer r.Remove(scratch)
	for _, sibling := range siblings {
		entry := path.Join(scratch, sibling)
		if err := r.MkdirAll(entry, 0700); err != nil {
			return ""
		}
		_, statErr := r.Lstat(path.Join(scratch, candidate))
		removeErr := r.Remove(entry)
		if statErr == nil && removeErr == nil {
			return sibling
		}
		if removeErr != nil {
			return ""
		}
	}
	return ""
}
