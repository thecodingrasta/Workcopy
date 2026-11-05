package workspace

import (
	"context"
	"fmt"
	"os"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
	"workcopy/internal/state"
)

// RecoverOptions selects optional, explicitly requested recovery behaviour.
type RecoverOptions struct {
	// AbandonReview drops the receipt of a review whose preparation never
	// finished. It deletes no files: the review directory is left for the
	// owner to remove. Use it when resuming preparation is impossible, for
	// example because someone edited the half-built review.
	AbandonReview bool
}

func (e *Engine) Recover(ctx context.Context, name string, dryRun bool) (Report, error) {
	return e.RecoverWith(ctx, name, RecoverOptions{}, dryRun)
}

// abandonPreparingReviews removes preparing receipts. A prepared, committing or
// committed review was handed to a human or contains a commit and is never
// abandoned here.
func (e *Engine) abandonPreparingReviews(ctx context.Context, store *state.Store, snapshot model.Snapshot, dryRun bool) (Report, error) {
	report := Report{Command: "recover"}
	abandoned := []orphanReview{}
	registered := e.registeredWorktrees(ctx)
	kept := []model.Review{}
	for _, receipt := range snapshot.Manifest.Reviews {
		if receipt.State != "preparing" {
			kept = append(kept, receipt)
			continue
		}
		if err := e.validateReviewPath(snapshot, receipt); err != nil {
			return report, err
		}
		abandoned = append(abandoned, orphanReview{Path: receipt.Path, Registered: registered[pathKey(receipt.Path)]})
	}
	if len(abandoned) == 0 {
		report.ExitCode = 4
		report.Message = "No review preparation is pending; nothing was abandoned."
		return report, nil
	}
	report.Plan = &Plan{Action: "abandon-review", Files: []string{snapshot.Manifest.Name}, Effects: []string{"Drop the receipt of each review that never finished preparing", "Delete no files; the review directories are left for the owner"}}
	e.adviseRetainedReviews(&report, abandoned)
	if dryRun {
		report.Message = "Unfinished review preparation would be abandoned; no changes were made."
		return report, nil
	}
	next := newGeneration(snapshot)
	next.Manifest.Reviews = kept
	if err := saveSnapshot(store, next); err != nil {
		return report, err
	}
	report.Workspace = view(next)
	report.Generation = next.Manifest.Generation
	report.Message = "Unfinished review preparation abandoned. The lane is usable again; run merge to prepare a fresh review."
	return report, nil
}

func (e *Engine) RecoverWith(ctx context.Context, name string, options RecoverOptions, dryRun bool) (Report, error) {
	report := Report{Command: "recover"}
	record, err := e.readRegistration(name)
	if err != nil {
		return report, err
	}
	if record.Phase == "ready" {
		if gone, goneErr := e.laneDirectoryGone(record); goneErr != nil {
			return report, goneErr
		} else if gone {
			// Dry-run must predict the real outcome, so it fails identically.
			return report, e.missingFailure(record)
		}
	}
	if options.AbandonReview {
		return e.abandonReview(ctx, record, dryRun)
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
	objects := newGitObjects(ctx, e.Repository)
	defer objects.Close()
	store.Objects = objects
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

func (e *Engine) abandonReview(ctx context.Context, record registration, dryRun bool) (Report, error) {
	report := Report{Command: "recover"}
	if record.Phase != "ready" {
		return report, model.Fail(3, "recovery-required", "Finish the interrupted creation or deletion with recover before abandoning a review")
	}
	if !dryRun {
		registry, registryLock, err := e.rootLock()
		if err != nil {
			return report, err
		}
		defer registry.Close()
		defer registryLock.Close()
	}
	root, err := fsops.OpenRoot(record.Path)
	if err != nil {
		return report, err
	}
	defer root.Close()
	store := state.New(root)
	if !dryRun {
		lock, lockErr := store.Lock()
		if lockErr != nil {
			return report, lockErr
		}
		defer lock.Close()
	}
	snapshot, err := store.Load()
	if err != nil {
		return report, err
	}
	m := snapshot.Manifest
	if m.ID != record.ID || m.WorkspacePath != record.Path || m.CommonGitDir != record.CommonGitDir {
		return report, model.Fail(3, "ownership-mismatch", "Recovery identities disagree")
	}
	if _, err = root.Lstat(state.JournalPath); err == nil {
		return report, model.Fail(3, "recovery-required", "An interrupted transaction exists; run recover without --abandon-review first")
	} else if !os.IsNotExist(err) {
		return report, err
	}
	return e.abandonPreparingReviews(ctx, store, snapshot, dryRun)
}
