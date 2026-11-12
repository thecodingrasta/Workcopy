// Package storage measures what a directory tree costs on disk and what the
// volume under it can do. It only reads: it never creates, links, moves or
// deletes anything, and it never follows a symbolic link or junction.
//
// Sizes come from the operating system's allocation figures, not from file
// lengths, and a file with several names is counted once. A tree that shares
// files with another place (a hard link into a package store, for example)
// reports those bytes as Shared, because deleting the tree would not free them.
//
// Block-cloned or copy-on-write data cannot be seen through these APIs, so on a
// volume that clones, Allocated is an upper bound.
package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Class names a kind of content so a total can be split by who owns it.
type Class string

// FileID identifies one file independent of how many names reach it.
type FileID struct {
	Volume uint64
	High   uint64
	Low    uint64
}

// FileFacts is what the operating system reports for one file.
type FileFacts struct {
	ID        FileID
	Links     uint32
	Logical   int64
	Allocated int64
}

// Totals is one row of a measurement.
type Totals struct {
	// Files counts names, so two hard links to one file count twice.
	Files int64 `json:"files"`
	// LinkedFiles counts names whose file has more than one name.
	LinkedFiles int64 `json:"hardLinkedFiles"`
	// LogicalBytes adds up file lengths per name.
	LogicalBytes int64 `json:"logicalBytes"`
	// AllocatedBytes is disk allocation with each file counted once.
	AllocatedBytes int64 `json:"allocatedBytes"`
	// ReclaimableBytes is the part of AllocatedBytes that deleting this tree
	// would free, because every name of the file lies inside the tree.
	ReclaimableBytes int64 `json:"reclaimableBytes"`
	// SharedBytes is the part of AllocatedBytes that a name outside the tree also
	// reaches. Deleting the tree would not free it.
	SharedBytes int64 `json:"sharedBytes"`
}

// Account is the result of measuring one tree.
type Account struct {
	Total   Totals            `json:"total"`
	Classes map[Class]*Totals `json:"classes"`
	// Links lists symbolic links and junctions that were seen and not entered.
	Links []string `json:"notEnteredLinks,omitempty"`
	// LinksOmitted counts links beyond the list limit.
	LinksOmitted int `json:"notEnteredLinksOmitted,omitempty"`
	// Unreadable counts files whose facts could not be read. They are not in any
	// total, so a nonzero value means the totals are a lower bound.
	Unreadable int `json:"unreadable,omitempty"`
	// Largest names the biggest top-level directory and class pairs by
	// allocation, so a full disk can be traced to a place.
	Largest []DirectoryTotal `json:"largestDirectories,omitempty"`
}

// DirectoryTotal is the cost of one top-level directory for one class. Files
// directly in the tree's root are grouped under ".".
type DirectoryTotal struct {
	Path           string `json:"path"`
	Class          Class  `json:"class"`
	Files          int64  `json:"files"`
	LogicalBytes   int64  `json:"logicalBytes"`
	AllocatedBytes int64  `json:"allocatedBytes"`
}

// maxLargest bounds the largest-directories list so a report stays small.
const maxLargest = 20

// Classifier assigns a tree-relative, slash-separated path to a Class.
type Classifier func(relative string) Class

// Walker visits a tree the way fs.WalkDir does, with slash-separated relative
// paths, without following links.
type Walker func(fs.WalkDirFunc) error

const maxListedLinks = 64

type identity struct {
	allocated int64
	links     uint32
	seen      uint32
	class     Class
}

// topLevel returns the first directory of a slash-separated path, or "." for a
// file directly in the root.
func topLevel(relative string) string {
	if index := strings.IndexByte(relative, '/'); index > 0 {
		return relative[:index]
	}
	return "."
}

func directoryRow(rows map[string]*DirectoryTotal, path string, class Class) *DirectoryTotal {
	key := path + "\x00" + string(class)
	row := rows[key]
	if row == nil {
		row = &DirectoryTotal{Path: path, Class: class}
		rows[key] = row
	}
	return row
}

// Measure walks a tree and accounts for its files. root is the directory the
// walker's paths are relative to. Cancellation returns the context error and no
// account. Unreadable files are counted and skipped, not fatal.
func Measure(ctx context.Context, root string, walk Walker, classify Classifier) (Account, error) {
	account := Account{Classes: map[Class]*Totals{}}
	identities := map[FileID]*identity{}
	directories := map[string]*DirectoryTotal{}
	row := func(class Class) *Totals {
		totals := account.Classes[class]
		if totals == nil {
			totals = &Totals{}
			account.Classes[class] = totals
		}
		return totals
	}
	err := walk(func(relative string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if relative == "." {
				return walkErr
			}
			account.Unreadable++
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if relative == "." || entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			// Symbolic links, junctions and special files: never entered, never sized.
			if len(account.Links) < maxListedLinks {
				account.Links = append(account.Links, relative)
			} else {
				account.LinksOmitted++
			}
			return nil
		}
		facts, factsErr := Facts(filepath.Join(root, filepath.FromSlash(relative)))
		if factsErr != nil {
			account.Unreadable++
			return nil
		}
		class := classify(relative)
		totals := row(class)
		totals.Files++
		totals.LogicalBytes += facts.Logical
		if facts.Links > 1 {
			totals.LinkedFiles++
		}
		place := directoryRow(directories, topLevel(relative), class)
		place.Files++
		place.LogicalBytes += facts.Logical
		known, ok := identities[facts.ID]
		if !ok {
			identities[facts.ID] = &identity{allocated: facts.Allocated, links: facts.Links, seen: 1, class: class}
			totals.AllocatedBytes += facts.Allocated
			place.AllocatedBytes += facts.Allocated
			return nil
		}
		known.seen++
		return nil
	})
	if err != nil {
		return Account{}, err
	}
	for _, found := range identities {
		totals := row(found.class)
		if found.links <= 1 || found.seen >= found.links {
			totals.ReclaimableBytes += found.allocated
		} else {
			totals.SharedBytes += found.allocated
		}
	}
	for _, place := range directories {
		account.Largest = append(account.Largest, *place)
	}
	sort.Slice(account.Largest, func(i, j int) bool {
		a, b := account.Largest[i], account.Largest[j]
		if a.AllocatedBytes != b.AllocatedBytes {
			return a.AllocatedBytes > b.AllocatedBytes
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Class < b.Class
	})
	if len(account.Largest) > maxLargest {
		account.Largest = account.Largest[:maxLargest]
	}
	names := make([]Class, 0, len(account.Classes))
	for class := range account.Classes {
		names = append(names, class)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	for _, class := range names {
		t := account.Classes[class]
		account.Total.Files += t.Files
		account.Total.LinkedFiles += t.LinkedFiles
		account.Total.LogicalBytes += t.LogicalBytes
		account.Total.AllocatedBytes += t.AllocatedBytes
		account.Total.ReclaimableBytes += t.ReclaimableBytes
		account.Total.SharedBytes += t.SharedBytes
	}
	return account, nil
}
