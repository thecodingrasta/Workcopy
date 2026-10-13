package workspace

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
	"workcopy/internal/state"
)

func (e *Engine) Destroy(ctx context.Context, name string, force, dryRun bool) (Report, error) {
	report := Report{Command: "destroy"}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	opened, err := e.openLane(name)
	if err != nil {
		return report, err
	}
	defer opened.close()
	var registry *fsops.Root
	var registryLock, laneLock *state.Lock
	if !dryRun {
		registry, registryLock, err = e.rootLock()
		if err != nil {
			return report, err
		}
		defer registry.Close()
		defer registryLock.Close()
		laneLock, err = e.reloadLocked(opened)
		if err != nil {
			return report, err
		}
		defer laneLock.Close()
	}
	if !force && len(opened.snapshot.Conflicts) > 0 {
		return report, model.Fail(3, "unresolved-work", "Resolve or explicitly discard conflicts before deletion")
	}
	record := opened.record
	record.Phase = "deleting"
	record.Force = force
	record.Quarantine = filepath.Join(e.WorkRoot, ".workcopy-trash-"+record.ID)
	record.DeleteFiles = map[string]model.FileVersion{}
	record.DeleteDirs = []string{}
	err = opened.root.Walk(func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			record.DeleteDirs = append(record.DeleteDirs, relative)
			return nil
		}
		metadata := strings.HasPrefix(relative, ".workcopy/")
		mapping := false
		for _, m := range opened.snapshot.Manifest.Mappings {
			if relative == m.Path {
				mapping = true
			}
		}
		if force {
			kind := model.Regular
			if entry.Type()&os.ModeSymlink != 0 {
				kind = model.Symlink
			}
			record.DeleteFiles[relative] = model.FileVersion{Kind: kind}
			return nil
		}
		before, known := opened.snapshot.Baseline.Files[relative]
		if !metadata && !mapping && !known {
			return model.Fail(3, "unexported-local-file", "Local-only file would be lost, including ignored files: "+relative+". Preserve it or explicitly use --force")
		}
		current, _, readErr := state.ReadVersion(opened.root, relative, before, opened.snapshot.Manifest.RespectExecutableBits)
		if readErr != nil {
			return readErr
		}
		if !metadata && !mapping && !before.Equal(current) {
			return model.Fail(3, "unexported-changes", "Unpromoted local changes would be lost: "+relative)
		}
		if mapping {
			target, readErr := opened.root.Readlink(relative)
			if readErr != nil {
				return readErr
			}
			for _, m := range opened.snapshot.Manifest.Mappings {
				if relative == m.Path && target != m.Target {
					return model.Fail(3, "mapping-retargeted", "Dependency mapping changed; nothing was deleted")
				}
			}
		}
		record.DeleteFiles[relative] = current
		return nil
	})
	if err != nil {
		return report, err
	}
	if !force {
		for path, baseline := range opened.snapshot.Baseline.Files {
			if baseline.Exists() {
				if _, found := record.DeleteFiles[path]; !found {
					return report, model.Fail(3, "unexported-deletion", "Unpromoted local deletion would be lost: "+path)
				}
			}
		}
	}
	report.Plan = &Plan{Action: "destroy", Files: []string{name}, Effects: []string{"Verify registered ownership and local contributions", "Quarantine the lane before deleting owned entries without following links", "Retain separate review worktrees"}}
	if dryRun {
		report.Message = "Deletion planned; no registry or filesystem changes were made."
		return report, nil
	}
	if err = saveRegistration(registry, record); err != nil {
		return report, err
	}
	laneLock.Close()
	opened.close()
	if err = leaveDestroyingLane(record.Path, e.WorkRoot); err != nil {
		return report, fmt.Errorf("cannot leave lane before deletion: %w", err)
	}
	if err = e.finishDeletion(ctx, registry, record); err != nil {
		return report, fmt.Errorf("deletion stopped with recoverable registry state: %w", err)
	}
	report.Message = "Owned lane removed. Shared targets and separate review worktrees were not deleted."
	return report, nil
}
func (e *Engine) finishDeletion(ctx context.Context, registry *fsops.Root, record registration) error {
	expected := filepath.Join(e.WorkRoot, ".workcopy-trash-"+record.ID)
	if record.Quarantine != expected {
		return fmt.Errorf("invalid quarantine identity")
	}
	_, quarantineErr := os.Lstat(expected)
	_, originalErr := os.Lstat(record.Path)
	if os.IsNotExist(quarantineErr) && originalErr == nil {
		if err := registry.Rename(record.Name, filepath.Base(expected)); err != nil {
			return err
		}
	} else if quarantineErr == nil && originalErr == nil {
		return model.Fail(3, "deletion-path-collision", "Both original and quarantine paths exist; neither was deleted")
	} else if os.IsNotExist(quarantineErr) && os.IsNotExist(originalErr) {
		if err := e.Repository.Unpin(ctx, record.ID); err != nil {
			return err
		}
		return registry.Remove(registrationPath(record.Name))
	} else if quarantineErr != nil && !os.IsNotExist(quarantineErr) {
		return quarantineErr
	}
	root, err := fsops.OpenRoot(expected)
	if err != nil {
		return err
	}
	dirs := map[string]bool{}
	for _, name := range record.DeleteDirs {
		dirs[name] = true
	}
	// Reject files created after quarantine. A failed deletion must not quietly
	// widen the original discard authorization on a later recovery attempt.
	err = root.Walk(func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		if entry.IsDir() {
			if !dirs[name] {
				return model.Fail(3, "quarantine-changed", "New directory in quarantine: "+name)
			}
			return nil
		}
		before, ok := record.DeleteFiles[name]
		if !ok {
			return model.Fail(3, "quarantine-changed", "New file in quarantine: "+name)
		}
		if !record.Force {
			current, _, readErr := state.ReadVersion(root, name, before, true)
			if readErr != nil {
				return readErr
			}
			if !current.Equal(before) {
				return model.Fail(3, "quarantine-changed", "Edited file in quarantine: "+name)
			}
		}
		return nil
	})
	if err != nil {
		root.Close()
		return err
	}
	names := []string{}
	for name := range record.DeleteFiles {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := strings.HasPrefix(names[i], ".workcopy/"), strings.HasPrefix(names[j], ".workcopy/")
		if a != b {
			return !a
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		if err = ctx.Err(); err != nil {
			root.Close()
			return err
		}
		if _, err = root.Lstat(name); os.IsNotExist(err) {
			continue
		} else if err != nil {
			root.Close()
			return err
		}
		if !record.Force {
			current, _, readErr := state.ReadVersion(root, name, record.DeleteFiles[name], true)
			if readErr != nil {
				root.Close()
				return readErr
			}
			if !current.Equal(record.DeleteFiles[name]) {
				root.Close()
				return model.Fail(3, "quarantine-changed", "Edited file preserved in quarantine: "+name)
			}
		}
		if err = root.Remove(name); err != nil {
			root.Close()
			return err
		}
	}
	sort.Slice(record.DeleteDirs, func(i, j int) bool {
		a, b := strings.Count(record.DeleteDirs[i], "/"), strings.Count(record.DeleteDirs[j], "/")
		if a != b {
			return a > b
		}
		return record.DeleteDirs[i] > record.DeleteDirs[j]
	})
	for _, name := range record.DeleteDirs {
		if err = root.Remove(name); err != nil && !os.IsNotExist(err) {
			root.Close()
			return err
		}
	}
	root.Close()
	if err = registry.Remove(filepath.Base(expected)); err != nil {
		return err
	}
	if err = e.Repository.Unpin(ctx, record.ID); err != nil {
		return err
	}
	return registry.Remove(registrationPath(record.Name))
}
