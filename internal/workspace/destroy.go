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
	if record, readErr := e.readRegistration(name); readErr == nil && record.Phase == "ready" {
		if gone, goneErr := e.laneDirectoryGone(record); goneErr != nil {
			return report, goneErr
		} else if gone {
			return e.destroyMissing(ctx, record, dryRun)
		}
	} else if readErr == nil && record.Phase == "creating" {
		return e.destroyUnfinished(ctx, record, force, dryRun)
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
	discarded := []string{}
	copied := opened.contextSet()
	contextEdited := 0
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
		if recorded, isContext := copied[relative]; isContext && !metadata && !mapping && !known {
			// A private copy of agent instructions from the main checkout.
			version, _, readErr := state.ReadVersion(opened.root, relative, model.FileVersion{}, false)
			if readErr != nil {
				return readErr
			}
			if version.Exists() && version.Hash != recorded.Hash {
				contextEdited++
			}
			record.DeleteFiles[relative] = version
			return nil
		}
		if !metadata && !mapping && !known && opened.discardable(relative) {
			// Build output the owner declared disposable. The content is still
			// recorded so deletion can tell if it changes while quarantined.
			version, _, readErr := state.ReadVersion(opened.root, relative, model.FileVersion{}, false)
			if readErr != nil {
				return readErr
			}
			record.DeleteFiles[relative] = version
			discarded = append(discarded, relative)
			return nil
		}
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
	if len(copied) > 0 {
		effect := fmt.Sprintf("Discard %d private agent-context copies (the originals in the main checkout are untouched)", len(copied))
		if contextEdited > 0 {
			effect += fmt.Sprintf("; %d were edited in the lane and those edits are lost", contextEdited)
		}
		report.Plan.Effects = append(report.Plan.Effects, effect)
	}
	if len(discarded) > 0 {
		report.Plan.Effects = append(report.Plan.Effects, fmt.Sprintf("Discard %d local-only files matching the owner's destroy.discard patterns (for example %s)", len(discarded), discarded[0]))
	}
	if !opened.snapshot.Manifest.OwnerConfig && opened.snapshot.Manifest.Config.Destroy != nil {
		report.Warnings = append(report.Warnings, "destroy.discard in a repository-committed configuration is ignored; only an owner-supplied --config can grant it.")
	}
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

// destroyUnfinished discards a lane whose creation never completed. Recover
// retries the creation, which cannot help when the cause is permanent (a name the
// volume cannot hold, a path it refuses), and every other command refuses a lane
// that is not ready, so without this the registration, its pinned ref and the
// partial directory could only be removed by editing Workcopy's registry by hand.
//
// Nothing in such a lane was ever handed to an agent or a person: creation did
// not return success. Even so, deleting a directory is destructive, so it needs
// the same explicit --force as discarding a lane's own changes, and it reuses the
// quarantine-then-verified-removal path of an ordinary deletion. Review
// worktrees cannot exist yet.
func (e *Engine) destroyUnfinished(ctx context.Context, record registration, force, dryRun bool) (Report, error) {
	report := Report{Command: "destroy"}
	if !force {
		return report, model.Fail(3, "recovery-required", "Lane creation did not finish. Run recover to retry it; if recover fails the same way, discard the unfinished lane with destroy "+record.Name+" --force")
	}
	if record.Path != filepath.Join(e.WorkRoot, record.Name) || record.CommonGitDir != e.Repository.CommonDir {
		return report, model.Fail(3, "ownership-mismatch", "The unfinished lane is not registered to this repository and lane root; nothing was deleted")
	}
	report.Plan = &Plan{Action: "destroy", Files: []string{record.Name}, Effects: []string{"Discard the partly created lane directory", "Remove its registration and pinned ref", "No review worktree exists for a lane that never finished creating"}}
	if dryRun {
		report.Message = "Discarding the unfinished lane planned; no registry or filesystem changes were made."
		return report, nil
	}
	registry, registryLock, err := e.rootLock()
	if err != nil {
		return report, err
	}
	defer registry.Close()
	defer registryLock.Close()
	// The phase can only move forward under the lock, so decide on a fresh read.
	record, err = e.readRegistration(record.Name)
	if err != nil {
		return report, err
	}
	if record.Phase != "creating" {
		return report, model.Fail(3, "recovery-required", "The lane changed state while it was being discarded; read its status and try again")
	}
	record.Phase = "deleting"
	record.Force = true
	record.Seed = nil
	record.Quarantine = filepath.Join(e.WorkRoot, ".workcopy-trash-"+record.ID)
	record.DeleteFiles = map[string]model.FileVersion{}
	record.DeleteDirs = []string{}
	if info, statErr := os.Lstat(record.Path); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return report, model.Fail(3, "ownership-mismatch", "The lane path is not a directory Workcopy created; nothing was deleted")
		}
		root, openErr := fsops.OpenRoot(record.Path)
		if openErr != nil {
			return report, openErr
		}
		walkErr := root.Walk(func(relative string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if relative == "." {
				return nil
			}
			if entry.IsDir() {
				record.DeleteDirs = append(record.DeleteDirs, relative)
				return nil
			}
			kind := model.Regular
			if entry.Type()&os.ModeSymlink != 0 {
				kind = model.Symlink
			}
			record.DeleteFiles[relative] = model.FileVersion{Kind: kind}
			return nil
		})
		root.Close()
		if walkErr != nil {
			return report, walkErr
		}
	} else if !os.IsNotExist(statErr) {
		return report, statErr
	}
	if err = saveRegistration(registry, record); err != nil {
		return report, err
	}
	if err = leaveDestroyingLane(record.Path, e.WorkRoot); err != nil {
		return report, fmt.Errorf("cannot leave lane before deletion: %w", err)
	}
	if err = e.finishDeletion(ctx, registry, record); err != nil {
		return report, fmt.Errorf("deletion stopped with recoverable registry state: %w", err)
	}
	report.Message = "Unfinished lane discarded: its partial directory, registration and pinned ref are gone."
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
			current, _, readErr := state.ReadVersion(root, name, before, false)
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
			current, _, readErr := state.ReadVersion(root, name, record.DeleteFiles[name], false)
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
