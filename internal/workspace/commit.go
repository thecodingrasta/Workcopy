package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"workcopy/internal/fsops"
	"workcopy/internal/git"
	"workcopy/internal/model"
	"workcopy/internal/state"
)

func (e *Engine) commitReview(ctx context.Context, name string, options MergeOptions, dryRun bool) (Report, error) {
	report := Report{Command: "merge"}
	if len(options.Message) > 65536 || strings.ContainsRune(options.Message, 0) || !utf8.ValidString(options.Message) {
		return report, model.Fail(2, "invalid-message", "Commit message must be nonempty UTF-8 text under 64 KiB without NUL")
	}
	opened, err := e.openLane(name)
	if err != nil {
		return report, err
	}
	defer opened.close()
	if !dryRun {
		lock, lockErr := e.reloadLocked(opened)
		if lockErr != nil {
			return report, lockErr
		}
		defer lock.Close()
	}
	var receipt *model.Review
	for _, r := range opened.snapshot.Manifest.Reviews {
		if r.ID == options.ReviewID {
			copy := r
			receipt = &copy
			break
		}
	}
	if receipt == nil || receipt.State != "prepared" {
		return report, model.Fail(3, "review-not-prepared", "No prepared review matches this receipt")
	}
	if err = e.validateReviewPath(opened.snapshot, *receipt); err != nil {
		return report, err
	}
	if err = e.verifyLaneReceipt(ctx, opened, *receipt); err != nil {
		return report, err
	}
	if err = e.verifyReviewGit(ctx, *receipt, receipt.BaseCommit); err != nil {
		return report, err
	}
	if err = e.verifyReviewFiles(*receipt, opened.snapshot.Manifest.RespectExecutableBits); err != nil {
		return report, err
	}
	if _, err = e.Repository.Run(ctx, receipt.Path, nil, "diff-index", "--cached", "--quiet", "--no-ext-diff", "--no-textconv", receipt.BaseCommit, "--"); err != nil {
		return report, model.Fail(3, "review-index-changed", "Review staging changed; Workcopy will not overwrite it")
	}
	report.Plan = &Plan{Action: "commit-review", Effects: []string{"Commit exactly the approved review using an isolated Git index", "Advance only the detached review HEAD; never main or a remote"}}
	if dryRun {
		report.Message = "Commit approval validated without creating Git objects, refs or metadata."
		return report, nil
	}
	directory, err := os.MkdirTemp("", "workcopy-index-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(directory)
	index := filepath.Join(directory, "index")
	root, err := fsops.OpenRoot(receipt.Path)
	if err != nil {
		return report, err
	}
	defer root.Close()
	names := []string{}
	for filename := range receipt.Files {
		names = append(names, filename)
	}
	sort.Strings(names)
	var indexEntries bytes.Buffer
	for _, filename := range names {
		version := receipt.Files[filename]
		oid := version.ObjectID
		if oid == "" {
			actual, body, readErr := state.ReadVersion(root, filename, version, opened.snapshot.Manifest.RespectExecutableBits)
			if readErr != nil {
				return report, readErr
			}
			if !actual.Equal(version) {
				return report, model.Fail(3, "review-changed", "Review changed while committing")
			}
			hashed, hashErr := e.Repository.Run(ctx, receipt.Path, body, "hash-object", "-w", "--no-filters", "--stdin")
			if hashErr != nil {
				return report, hashErr
			}
			oid = strings.TrimSpace(string(hashed))
		}
		if !git.ValidObjectID(oid) {
			return report, fmt.Errorf("invalid review object ID")
		}
		mode := uint32(0100644)
		if version.Kind == model.Symlink {
			mode = 0120000
		} else if version.Mode == 0755 {
			mode = 0100755
		}
		fmt.Fprintf(&indexEntries, "%o %s\t%s%c", mode, oid, filename, 0)
	}
	if _, err = e.Repository.RunIndex(ctx, receipt.Path, index, nil, "read-tree", "--empty"); err != nil {
		return report, err
	}
	if _, err = e.Repository.RunIndex(ctx, receipt.Path, index, indexEntries.Bytes(), "update-index", "-z", "--index-info"); err != nil {
		return report, err
	}
	tree, err := e.Repository.RunIndex(ctx, receipt.Path, index, nil, "write-tree")
	if err != nil {
		return report, err
	}
	treeID := strings.TrimSpace(string(tree))
	if !git.ValidObjectID(treeID) {
		return report, fmt.Errorf("invalid review tree ID")
	}
	commit, err := e.Repository.Run(ctx, receipt.Path, []byte(options.Message+"\n"), "commit-tree", treeID, "-p", receipt.BaseCommit)
	if err != nil {
		return report, fmt.Errorf("configure Git author identity before committing: %w", err)
	}
	receipt.Commit = strings.TrimSpace(string(commit))
	if !git.ValidObjectID(receipt.Commit) {
		return report, fmt.Errorf("invalid commit ID")
	}
	receipt.State = "committing"
	if _, err = replaceReview(opened.store, *receipt); err != nil {
		return report, err
	}
	if err = e.finishCommit(ctx, opened.store, *receipt); err != nil {
		return report, fmt.Errorf("commit recovery receipt preserved; run recover: %w", err)
	}
	fresh, err := opened.store.Load()
	if err != nil {
		return report, err
	}
	for _, r := range fresh.Manifest.Reviews {
		if r.ID == receipt.ID {
			report.Review = &r
			break
		}
	}
	report.Workspace = view(fresh)
	report.Generation = fresh.Manifest.Generation
	report.Message = "Approved review committed on its detached HEAD. Main was not changed and nothing was pushed."
	return report, nil
}
func (e *Engine) finishCommit(ctx context.Context, store *state.Store, receipt model.Review) error {
	snapshot, err := store.Load()
	if err != nil {
		return err
	}
	if err = e.validateReviewPath(snapshot, receipt); err != nil {
		return err
	}
	if !git.ValidObjectID(receipt.Commit) {
		return fmt.Errorf("invalid pending commit")
	}
	if err = e.verifyReviewFiles(receipt, snapshot.Manifest.RespectExecutableBits); err != nil {
		return err
	}
	current, err := e.Repository.Run(ctx, receipt.Path, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	head := strings.TrimSpace(string(current))
	if head != receipt.BaseCommit && head != receipt.Commit {
		return model.Fail(3, "review-head-changed", "Review HEAD diverged during commit recovery")
	}
	if err = e.verifyReviewGit(ctx, receipt, head); err != nil {
		return err
	}
	// Replay may encounter the old or already updated index, never an unrelated
	// staging state created after approval.
	_, baseIndexErr := e.Repository.Run(ctx, receipt.Path, nil, "diff-index", "--cached", "--quiet", "--no-ext-diff", "--no-textconv", receipt.BaseCommit, "--")
	if baseIndexErr != nil {
		if _, err = e.Repository.Run(ctx, receipt.Path, nil, "diff-index", "--cached", "--quiet", "--no-ext-diff", "--no-textconv", receipt.Commit, "--"); err != nil {
			return model.Fail(3, "review-index-changed", "Review staging changed during recovery; it was preserved")
		}
	}
	if head == receipt.BaseCommit {
		if _, err = e.Repository.Run(ctx, receipt.Path, nil, "update-ref", "--no-deref", "HEAD", receipt.Commit, receipt.BaseCommit); err != nil {
			return err
		}
	}
	if _, err = e.Repository.Run(ctx, receipt.Path, nil, "read-tree", receipt.Commit); err != nil {
		return err
	}
	receipt.State = "committed"
	_, err = replaceReview(store, receipt)
	return err
}
