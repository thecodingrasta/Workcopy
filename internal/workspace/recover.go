package workspace

import (
	"context"
	"fmt"
	"os"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
	"workcopy/internal/state"
)

func (e *Engine) Recover(ctx context.Context, name string, dryRun bool) (Report, error) {
	report := Report{Command: "recover"}
	record, err := e.readRegistration(name)
	if err != nil {
		return report, err
	}
	if dryRun {
		report.Message = "Recovery would revalidate the owned creation, transaction or deletion record. No writes were made."
		return report, nil
	}
	registry, registryLock, err := e.rootLock()
	if err != nil {
		return report, err
	}
	defer registry.Close()
	defer registryLock.Close()
	record, err = e.readRegistration(name)
	if err != nil {
		return report, err
	}
	if record.Phase == "deleting" {
		if err = e.finishDeletion(ctx, registry, record); err != nil {
			return report, err
		}
		report.Message = "Interrupted deletion completed without following directory links."
		return report, nil
	}
	if record.Phase == "creating" {
		if err = registry.MkdirAll(name, 0700); err != nil {
			return report, err
		}
		if err = e.finishCreation(ctx, record); err != nil {
			return report, err
		}
		record.Phase = "ready"
		record.Seed = nil
		if err = saveRegistration(registry, record); err != nil {
			return report, err
		}
		report.Message = "Interrupted creation recovered."
		return report, nil
	}
	root, err := fsops.OpenRoot(record.Path)
	if err != nil {
		return report, err
	}
	defer root.Close()
	store := state.New(root)
	snapshot, err := store.Load()
	if err != nil {
		return report, err
	}
	if snapshot.Manifest.ID != record.ID || snapshot.Manifest.WorkspacePath != record.Path || snapshot.Manifest.CommonGitDir != record.CommonGitDir {
		return report, model.Fail(3, "ownership-mismatch", "Recovery identities disagree")
	}
	lock, err := store.Lock()
	if err != nil {
		return report, err
	}
	defer lock.Close()
	snapshot, err = store.Load()
	if err != nil {
		return report, err
	}
	if snapshot.Manifest.ID != record.ID || snapshot.Manifest.WorkspacePath != record.Path || snapshot.Manifest.CommonGitDir != record.CommonGitDir {
		return report, model.Fail(3, "ownership-mismatch", "Recovery identities changed while acquiring the lock")
	}
	if _, err = root.Lstat(state.JournalPath); os.IsNotExist(err) {
		for _, receipt := range snapshot.Manifest.Reviews {
			if receipt.State == "preparing" {
				if err = e.finishReview(ctx, store, receipt); err != nil {
					return report, err
				}
				report.Message = "Interrupted review preparation recovered."
				return report, nil
			}
			if receipt.State == "committing" {
				if err = e.finishCommit(ctx, store, receipt); err != nil {
					return report, err
				}
				report.Message = "Interrupted review commit recovered."
				return report, nil
			}
		}
		report.ExitCode = 4
		report.Message = "No incomplete transaction was found."
		return report, nil
	} else if err != nil {
		return report, err
	}
	if err = store.Recover(); err != nil {
		return report, fmt.Errorf("recovery preserved the lane: %w", err)
	}
	snapshot, err = store.Load()
	if err != nil {
		return report, err
	}
	report.Workspace = view(snapshot)
	report.Generation = snapshot.Manifest.Generation
	report.Message = "Transaction recovered. Later user edits were not overwritten."
	return report, nil
}
