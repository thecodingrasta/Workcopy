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
	"workcopy/internal/git"
	"workcopy/internal/model"
	"workcopy/internal/project"
	"workcopy/internal/state"
)

type MergeOptions struct {
	Commit, Approve   bool
	ReviewID, Message string
}

func (e *Engine) Merge(ctx context.Context, name string, options MergeOptions, dryRun bool) (Report, error) {
	report := Report{Command: "merge"}
	if options.Commit {
		if !options.Approve || options.ReviewID == "" || strings.TrimSpace(options.Message) == "" {
			return report, model.Fail(2, "commit-approval-required", "Commit requires --approve, --review RECEIPT and --message MESSAGE")
		}
		return e.commitReview(ctx, name, options, dryRun)
	}
	if options.Approve || options.ReviewID != "" || options.Message != "" {
		return report, model.Fail(2, "commit-mode-required", "Approval, receipt and message options require --commit")
	}
	if dryRun {
		status, err := e.Status(ctx, name)
		if err != nil {
			return report, err
		}
		report.Workspace = status.Workspace
		report.Conflicts = status.Conflicts
		report.ExitCode = status.ExitCode
		report.Plan = &Plan{Action: "merge", Effects: []string{"Reconcile upstream before preparing an unstaged separate review worktree", "Do not create a commit or modify the source checkout"}}
		report.Message = "Review preparation planned. Text-merge outcomes are evaluated during apply."
		return report, nil
	}
	synced, err := e.Sync(ctx, name, false)
	if err != nil {
		return report, err
	}
	if len(synced.Conflicts) > 0 {
		synced.Command = "merge"
		return synced, nil
	}
	registry, registryLock, err := e.rootLock()
	if err != nil {
		return report, err
	}
	defer registry.Close()
	defer registryLock.Close()
	opened, err := e.openLane(name)
	if err != nil {
		return report, err
	}
	defer opened.close()
	lock, err := e.reloadLocked(opened)
	if err != nil {
		return report, err
	}
	defer lock.Close()
	_, commit, err := e.Repository.Resolve(ctx, opened.snapshot.Manifest.Branch)
	if err != nil {
		return report, err
	}
	if commit != opened.snapshot.Manifest.SyncedCommit {
		return report, model.Fail(3, "upstream-moved", "Upstream advanced after reconciliation; rerun merge")
	}
	current, err := e.scan(ctx, opened)
	if err != nil {
		return report, err
	}
	delta := changes(opened.snapshot.Baseline.Files, current)
	if len(delta) == 0 {
		report.ExitCode = 4
		report.Message = "No local source changes need review."
		return report, nil
	}
	for _, receipt := range opened.snapshot.Manifest.Reviews {
		if receipt.State == "prepared" && receipt.Generation == opened.snapshot.Manifest.Generation {
			if err = e.verifyLaneReceipt(ctx, opened, receipt); err == nil {
				if err = e.verifyReviewGit(ctx, receipt, receipt.BaseCommit); err != nil {
					return report, err
				}
				if err = e.verifyReviewFiles(receipt, opened.snapshot.Manifest.RespectExecutableBits); err != nil {
					return report, err
				}
				report.Review = &receipt
				report.Message = "Existing unchanged review reused."
				return report, nil
			}
		}
	}
	tree, err := e.Repository.Tree(ctx, commit)
	if err != nil {
		return report, err
	}
	names, err := validateTree(tree, model.Selection{All: true}, false)
	if err != nil {
		return report, err
	}
	if err = e.validateAttributes(ctx, commit, names); err != nil {
		return report, err
	}
	id, err := model.ID()
	if err != nil {
		return report, err
	}
	receipt := model.Review{ID: id, Path: filepath.Join(e.WorkRoot, ".workcopy-reviews", name+"-"+id), State: "preparing", BaseCommit: commit, Files: map[string]model.FileVersion{}}
	reader, err := e.Repository.Reader(ctx)
	if err != nil {
		return report, err
	}
	defer reader.Close()
	for _, filename := range names {
		body, readErr := reader.Read(tree[filename].ObjectID)
		if readErr != nil {
			return report, readErr
		}
		version, versionErr := entryVersion(tree[filename], body)
		if versionErr != nil {
			return report, versionErr
		}
		if version.Kind == model.Symlink {
			if err = validateLink(filename, string(body)); err != nil {
				return report, err
			}
		}
		receipt.Files[filename] = version
	}
	for _, change := range delta {
		version, body, readErr := state.ReadVersion(opened.root, change.Path, opened.snapshot.Baseline.Files[change.Path], opened.snapshot.Manifest.RespectExecutableBits)
		if readErr != nil {
			return report, readErr
		}
		if !version.Equal(current[change.Path]) {
			return report, model.Fail(3, "file-changed", "Lane changed while preparing review")
		}
		if !version.Exists() {
			delete(receipt.Files, change.Path)
			continue
		}
		if version.Kind == model.Symlink {
			if err = validateLink(change.Path, string(body)); err != nil {
				return report, err
			}
		}
		if _, err = opened.store.PutBlob(body); err != nil {
			return report, err
		}
		receipt.Files[change.Path] = version
	}
	if err = noGitAncestor(filepath.Dir(receipt.Path)); err != nil {
		return report, err
	}
	if err = registry.MkdirAll(".workcopy-reviews", 0700); err != nil {
		return report, err
	}
	next := newGeneration(opened.snapshot)
	receipt.Generation = next.Manifest.Generation
	next.Manifest.Reviews = append(next.Manifest.Reviews, receipt)
	if err = saveSnapshot(opened.store, next); err != nil {
		return report, err
	}
	if err = e.finishReview(ctx, opened.store, receipt); err != nil {
		return report, fmt.Errorf("review preparation preserved its receipt; run recover: %w", err)
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
	report.Message = "Unstaged review worktree prepared. Review its diff before an explicit commit. Main checkout was not changed."
	return report, nil
}
func saveSnapshot(store *state.Store, next model.Snapshot) error {
	tx, err := store.Begin(next)
	if err != nil {
		return err
	}
	if err = tx.Prepare(); err != nil {
		return err
	}
	return tx.Apply()
}
func replaceReview(store *state.Store, receipt model.Review) (model.Snapshot, error) {
	snapshot, err := store.Load()
	if err != nil {
		return snapshot, err
	}
	next := newGeneration(snapshot)
	found := false
	receipt.Generation = next.Manifest.Generation
	for index, existing := range next.Manifest.Reviews {
		if existing.ID == receipt.ID {
			next.Manifest.Reviews[index] = receipt
			found = true
			break
		}
	}
	if !found {
		return snapshot, fmt.Errorf("review receipt not found")
	}
	return next, saveSnapshot(store, next)
}
func (e *Engine) validateReviewPath(snapshot model.Snapshot, receipt model.Review) error {
	if !model.ValidID(receipt.ID) || receipt.Path != filepath.Join(e.WorkRoot, ".workcopy-reviews", snapshot.Manifest.Name+"-"+receipt.ID) {
		return model.Fail(3, "review-ownership-mismatch", "Review path does not match its receipt")
	}
	return nil
}
func (e *Engine) finishReview(ctx context.Context, store *state.Store, receipt model.Review) error {
	snapshot, err := store.Load()
	if err != nil {
		return err
	}
	if err = e.validateReviewPath(snapshot, receipt); err != nil {
		return err
	}
	if _, err = os.Lstat(receipt.Path); os.IsNotExist(err) {
		if _, err = e.Repository.Run(ctx, e.Repository.Root, nil, "worktree", "add", "--detach", "--no-checkout", receipt.Path, receipt.BaseCommit); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err = e.verifyReviewGit(ctx, receipt, receipt.BaseCommit); err != nil {
		return err
	}
	root, err := fsops.OpenRoot(receipt.Path)
	if err != nil {
		return err
	}
	defer root.Close()
	reader, err := e.Repository.Reader(ctx)
	if err != nil {
		return err
	}
	defer reader.Close()
	// Only missing files or exact prior writes are resumable. External review
	// edits are never overwritten to make a receipt appear complete.
	err = root.Walk(func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." || name == ".git" || entry.IsDir() {
			return nil
		}
		version, ok := receipt.Files[name]
		if !ok {
			return model.Fail(3, "review-changed", "Unexpected file in interrupted review: "+name)
		}
		current, _, readErr := state.ReadVersion(root, name, version, snapshot.Manifest.RespectExecutableBits)
		if readErr != nil {
			return readErr
		}
		if !current.Equal(version) {
			return model.Fail(3, "review-changed", "Edited file in interrupted review: "+name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	names := []string{}
	for name := range receipt.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err = ctx.Err(); err != nil {
			return err
		}
		version := receipt.Files[name]
		current, _, readErr := state.ReadVersion(root, name, version, snapshot.Manifest.RespectExecutableBits)
		if readErr != nil {
			return readErr
		}
		if current.Equal(version) {
			continue
		}
		if current.Exists() {
			return model.Fail(3, "review-changed", "Review file changed during materialization")
		}
		body, bodyErr := versionBody(reader, store, version)
		if bodyErr != nil {
			return bodyErr
		}
		if version.Kind == model.Symlink {
			err = root.ReplaceSymlink(name, string(body), false)
		} else {
			err = root.WriteAtomic(name, body, os.FileMode(version.Mode))
		}
		if err != nil {
			return err
		}
	}
	if err = e.verifyReviewFiles(receipt, snapshot.Manifest.RespectExecutableBits); err != nil {
		return err
	}
	// A missing index is expected for a newly added --no-checkout worktree.
	// An existing index must still represent the base; replay never erases staging.
	indexPath, indexErr := e.Repository.Run(ctx, receipt.Path, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if indexErr != nil {
		return indexErr
	}
	if _, indexErr = os.Lstat(strings.TrimSpace(string(indexPath))); indexErr == nil {
		if _, indexErr = e.Repository.Run(ctx, receipt.Path, nil, "diff-index", "--cached", "--quiet", "--no-ext-diff", "--no-textconv", receipt.BaseCommit, "--"); indexErr != nil {
			return model.Fail(3, "review-index-changed", "Review staging changed; recovery will not overwrite it")
		}
	} else if !os.IsNotExist(indexErr) {
		return indexErr
	}
	if _, err = e.Repository.Run(ctx, receipt.Path, nil, "read-tree", receipt.BaseCommit); err != nil {
		return err
	}
	receipt.State = "prepared"
	_, err = replaceReview(store, receipt)
	return err
}
func (e *Engine) verifyReviewGit(ctx context.Context, receipt model.Review, head string) error {
	root, err := fsops.OpenRoot(receipt.Path)
	if err != nil {
		return err
	}
	info, err := root.Lstat(".git")
	root.Close()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return model.Fail(3, "review-ownership-mismatch", "Review .git entry is not a regular gitfile")
	}
	repository, err := git.Open(ctx, receipt.Path)
	if err != nil {
		return err
	}
	if repository.Root != receipt.Path || repository.CommonDir != e.Repository.CommonDir {
		return model.Fail(3, "review-ownership-mismatch", "Review is not a worktree of the registered repository")
	}
	if body, symbolErr := repository.Run(ctx, receipt.Path, nil, "symbolic-ref", "-q", "HEAD"); symbolErr == nil || len(body) > 0 {
		return model.Fail(3, "review-head-attached", "Review HEAD was attached to a branch; Workcopy will not update it")
	}
	body, err := repository.Run(ctx, receipt.Path, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(body)) != head {
		return model.Fail(3, "review-head-changed", "Review HEAD changed since preparation")
	}
	return nil
}
func (e *Engine) verifyLaneReceipt(ctx context.Context, opened *lane, receipt model.Review) error {
	if receipt.BaseCommit != opened.snapshot.Manifest.SyncedCommit || receipt.Generation != opened.snapshot.Manifest.Generation || len(opened.snapshot.Conflicts) > 0 {
		return model.Fail(3, "stale-review", "Lane generation or baseline changed; prepare a fresh review")
	}
	current, err := e.scan(ctx, opened)
	if err != nil {
		return err
	}
	keys := map[string]bool{}
	for name := range current {
		keys[name] = true
	}
	for name := range receipt.Files {
		if project.Selected(opened.snapshot.Manifest.Selection, name) {
			keys[name] = true
		}
	}
	for name := range keys {
		if !current[name].Equal(receipt.Files[name]) {
			return model.Fail(3, "stale-review", "Lane content differs from the review receipt: "+name)
		}
	}
	return nil
}
func (e *Engine) verifyReviewFiles(receipt model.Review, respect bool) error {
	root, err := fsops.OpenRoot(receipt.Path)
	if err != nil {
		return err
	}
	defer root.Close()
	seen := map[string]bool{}
	err = root.Walk(func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." || name == ".git" || entry.IsDir() {
			return nil
		}
		expected, ok := receipt.Files[name]
		if !ok {
			return model.Fail(3, "review-changed", "Unexpected review file, including ignored files: "+name)
		}
		actual, _, readErr := state.ReadVersion(root, name, expected, respect)
		if readErr != nil {
			return readErr
		}
		if !expected.Equal(actual) {
			return model.Fail(3, "review-changed", "Review content changed: "+name)
		}
		seen[name] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(receipt.Files) {
		return model.Fail(3, "review-changed", "Review files were deleted")
	}
	return nil
}
