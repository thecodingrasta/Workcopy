package state

import (
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"workcopy/internal/model"
)

// File/directory changes are authorized as individual file operations. Only
// empty directories may be removed, and every existing descendant file must
// have its own checked deletion in this transaction.
func (t *Transaction) validateWriteShape(op Operation, operations map[string]Operation) error {
	parts := strings.Split(op.Path, "/")
	for n := 1; n < len(parts); n++ {
		parent := strings.Join(parts[:n], "/")
		planned, hasPlan := operations[parent]
		if hasPlan && planned.After.Exists() {
			return fmt.Errorf("transaction creates both a file and its descendant: %s", parent)
		}
		info, err := t.store.Root.Lstat(parent)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() && (!hasPlan || planned.After.Exists()) {
			return model.Fail(3, "path-obstructed", "A parent file is not authorized for removal: "+parent)
		}
	}
	info, err := t.store.Root.Lstat(op.Path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return t.walkDestination(op.Path, func(name string, entry fs.DirEntry) error {
		if entry.IsDir() {
			return nil
		}
		deletion, ok := operations[name]
		if !ok || deletion.After.Exists() {
			return model.Fail(3, "directory-obstructed", "Directory replacement would discard an unplanned file: "+name)
		}
		return nil
	})
}
func (t *Transaction) walkDestination(destination string, visit func(string, fs.DirEntry) error) error {
	return t.store.Root.Walk(func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if name == destination || strings.HasPrefix(name, destination+"/") {
			return visit(name, entry)
		}
		if entry.IsDir() && !strings.HasPrefix(destination, name+"/") {
			return fs.SkipDir
		}
		return nil
	})
}
func (t *Transaction) removeEmptyDestination(name string) error {
	info, err := t.store.Root.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	directories := []string{}
	if err = t.walkDestination(name, func(relative string, entry fs.DirEntry) error {
		if !entry.IsDir() {
			return model.Fail(3, "directory-changed", "A file appeared during directory replacement; preserved: "+relative)
		}
		directories = append(directories, relative)
		return nil
	}); err != nil {
		return err
	}
	sort.Slice(directories, func(i, j int) bool { return len(directories[i]) > len(directories[j]) })
	for _, directory := range directories {
		if err = t.store.Root.Remove(directory); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
