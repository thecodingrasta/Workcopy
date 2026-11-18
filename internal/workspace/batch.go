package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"workcopy/internal/fsops"
	"workcopy/internal/git"
	"workcopy/internal/model"
)

type BatchReview struct {
	ID     string   `json:"id"`
	Lanes  []string `json:"lanes"`
	Target string   `json:"target"`
	Base   string   `json:"base"`
	Commit string   `json:"commit,omitempty"`
	Path   string   `json:"path,omitempty"`
	State  string   `json:"state"`
}

type batchReceipt struct {
	BatchReview
	Repository string       `json:"repository"`
	Checkout   string       `json:"checkout"`
	Inputs     []batchInput `json:"inputs"`
}

func batchPath(id string) string { return ".workcopy-batches/" + id + ".json" }

func (e *Engine) batchInputs(names []string) ([]*lane, func(), error) {
	if len(names) == 0 || len(names) > 100 {
		return nil, nil, model.Fail(2, "invalid-batch-selection", "Select between 1 and 100 lane names")
	}
	var cleanup []func()
	closeInputs := func() {
		for i := len(cleanup) - 1; i >= 0; i-- {
			cleanup[i]()
		}
	}
	opened := make([]*lane, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		key := strings.ToLower(name)
		if seen[key] {
			closeInputs()
			return nil, nil, model.Fail(2, "duplicate-batch-lane", "Lane appears more than once: "+name)
		}
		seen[key] = true
		item, err := e.openLane(name)
		if err != nil {
			closeInputs()
			return nil, nil, err
		}
		cleanup = append(cleanup, item.close)
		lock, err := e.reloadLocked(item)
		if err != nil {
			closeInputs()
			return nil, nil, err
		}
		cleanup = append(cleanup, func() { lock.Close() })
		opened = append(opened, item)
	}
	return opened, closeInputs, nil
}

func (e *Engine) batchTarget(ctx context.Context, target string) (string, string, error) {
	ref, base, err := e.Repository.Resolve(ctx, target)
	if err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(ref, "refs/heads/") {
		return "", "", model.Fail(2, "batch-target-not-local", "Choose a local branch for the batch target")
	}
	return ref, base, nil
}

// PrepareBatch combines selected lane snapshots in their given order. The
// candidate is isolated from the source checkout and pinned by its review HEAD.
func (e *Engine) PrepareBatch(ctx context.Context, names []string, target string, dryRun bool) (Report, error) {
	return e.PrepareBatchWith(ctx, names, target, nil, dryRun)
}

func (e *Engine) PrepareBatchWith(ctx context.Context, names []string, target string, resolutions []BatchResolution, dryRun bool) (Report, error) {
	return e.PrepareBatchWithAdmission(ctx, names, target, resolutions, dryRun, nil)
}

// PrepareBatchWithAdmission admits the exact target and locked lane inputs
// that will be used to build the candidate, before creating Git objects.
func (e *Engine) PrepareBatchWithAdmission(ctx context.Context, names []string, target string, resolutions []BatchResolution, dryRun bool, admit func(Plan) error) (Report, error) {
	report := Report{Command: "batch-review"}
	choices, err := batchResolutions(resolutions)
	if err != nil {
		return report, err
	}
	ref, base, err := e.batchTarget(ctx, target)
	if err != nil {
		return report, err
	}
	entries, err := e.Repository.Tree(ctx, base)
	if err != nil {
		return report, err
	}
	files, err := validateTree(entries, model.Selection{All: true}, false)
	if err != nil {
		return report, err
	}
	var size int64
	for _, entry := range entries {
		size += entry.Size
	}
	for _, choice := range choices {
		size += int64(len(choice.content))
	}
	report.Plan = &Plan{Action: "batch-review", Files: files, FileCount: len(files), SourceBytes: size}
	additions := map[string]bool{}
	budgetPath := func(path string, size int64) {
		report.Plan.SourceBytes += size
		if _, tracked := entries[path]; !tracked && !additions[path] {
			additions[path] = true
			report.Plan.Files = append(report.Plan.Files, path)
			report.Plan.FileCount++
		}
	}
	if dryRun {
		if len(names) == 0 || len(names) > 100 {
			return report, model.Fail(2, "invalid-batch-selection", "Select between 1 and 100 lane names")
		}
		seen := map[string]bool{}
		for _, name := range names {
			key := strings.ToLower(name)
			if seen[key] {
				return report, model.Fail(2, "duplicate-batch-lane", "Lane appears more than once: "+name)
			}
			seen[key] = true
			status, statusErr := e.Status(ctx, name)
			if statusErr != nil {
				return report, statusErr
			}
			if status.ExitCode == 1 {
				return report, model.Fail(1, "batch-lane-conflicts", "Resolve recorded conflicts in lane "+name+" before preparing the batch")
			}
			laneRoot, openErr := fsops.OpenRoot(status.Workspace.Path)
			if openErr != nil {
				return report, openErr
			}
			for _, file := range status.Changes {
				if file.Kind == "deleted" {
					continue
				}
				info, statErr := laneRoot.Lstat(file.Path)
				if statErr != nil {
					laneRoot.Close()
					return report, statErr
				}
				budgetPath(file.Path, info.Size())
			}
			laneRoot.Close()
		}
		report.Message = "Batch storage estimated. Reconciliation runs during review preparation."
		return report, nil
	}
	root, registry, err := e.rootLock()
	if err != nil {
		return report, err
	}
	defer root.Close()
	defer registry.Close()
	opened, closeInputs, err := e.batchInputs(names)
	if err != nil {
		return report, err
	}
	defer closeInputs()
	inputs := make([]batchInput, 0, len(names))
	deltas := make([][]batchDelta, 0, len(names))
	for _, item := range opened {
		input, delta, inspectErr := e.inspectBatchInput(ctx, item)
		if inspectErr != nil {
			return report, inspectErr
		}
		inputs = append(inputs, input)
		deltas = append(deltas, delta)
		for _, edit := range delta {
			if edit.Local.Exists() {
				budgetPath(edit.Path, edit.Local.Size)
			}
		}
	}
	if admit != nil {
		if err = admit(*report.Plan); err != nil {
			return report, err
		}
	}
	_, current, err := e.batchTarget(ctx, ref)
	if err != nil {
		return report, err
	}
	if current != base {
		return report, model.Fail(3, "batch-target-changed", "The target advanced during batch planning. Prepare again against the new target.")
	}
	reader, err := e.Repository.Reader(ctx)
	if err != nil {
		return report, err
	}
	defer reader.Close()
	tree := batchTree{target: entries, tree: maps.Clone(entries), reader: reader, base: base, resolutions: choices}
	for i, item := range opened {
		delta := deltas[i]
		for _, edit := range delta {
			if err = e.applyBatchDelta(ctx, item, &tree, edit); err != nil {
				var conflict *BatchConflict
				if errors.As(err, &conflict) {
					report.BatchConflict = conflict
				}
				return report, err
			}
		}
	}
	if len(choices) != 0 {
		return report, model.Fail(3, "stale-batch-resolution", "A resolution no longer matches a conflict. Prepare again with current tokens.")
	}
	if err = tree.verifyShape(); err != nil {
		return report, err
	}
	files, err = validateTree(tree.tree, model.Selection{All: true}, false)
	if err != nil {
		return report, err
	}
	edits := tree.indexEdits()
	if len(edits) == 0 {
		report.ExitCode, report.Message = 4, "Selected lanes add no changes to the target."
		return report, nil
	}
	commit, err := e.writeBatchCommit(ctx, base, edits, names)
	if err != nil {
		return report, err
	}
	id, err := model.ID()
	if err != nil {
		return report, err
	}
	review := BatchReview{ID: id, Lanes: names, Target: ref, Base: base, Commit: commit, Path: filepath.Join(e.WorkRoot, ".workcopy-reviews", "batch-"+id), State: "preparing"}
	receipt := batchReceipt{BatchReview: review, Repository: e.Repository.CommonDir, Checkout: e.Repository.Root, Inputs: inputs}
	if err = root.MkdirAll(".workcopy-reviews", 0700); err != nil {
		return report, err
	}
	if err = noGitAncestor(filepath.Dir(review.Path)); err != nil {
		return report, err
	}
	if err = saveBatch(root, receipt); err != nil {
		return report, err
	}
	report.Batch = &receipt.BatchReview
	if err = e.finishBatchReview(ctx, receipt, reader, tree.tree, files); err != nil {
		return report, err
	}
	receipt.State = "prepared"
	if err = saveBatch(root, receipt); err != nil {
		return report, err
	}
	report.Message = "Combined review prepared. Inspect the base-to-candidate diff and validate before approval."
	return report, nil
}

func (e *Engine) writeBatchCommit(ctx context.Context, base string, edits []byte, names []string) (string, error) {
	if err := e.requireGitIdentity(ctx, e.Repository.Root); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "workcopy-batch-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	repo := e.Repository
	index := filepath.Join(dir, "index")
	if _, err = repo.RunIndex(ctx, repo.Root, index, nil, "read-tree", base); err != nil {
		return "", err
	}
	if _, err = repo.RunIndex(ctx, repo.Root, index, edits, "update-index", "-z", "--index-info"); err != nil {
		return "", err
	}
	tree, err := repo.RunIndex(ctx, repo.Root, index, nil, "write-tree")
	if err != nil {
		return "", err
	}
	treeID := strings.TrimSpace(string(tree))
	if !git.ValidObjectID(treeID) {
		return "", fmt.Errorf("invalid batch tree ID")
	}
	message := []byte("Integrate reviewed Workcopy lanes\n\nLanes: " + strings.Join(names, ", ") + "\n")
	body, err := repo.Run(ctx, repo.Root, message, "commit-tree", treeID, "-p", base)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(body))
	if !git.ValidObjectID(id) {
		return "", fmt.Errorf("invalid batch commit ID")
	}
	return id, nil
}

func (e *Engine) batchInventory(ctx context.Context, review BatchReview, reader *git.Reader, tree map[string]git.TreeEntry, names []string) (model.Review, error) {
	inventory := model.Review{Path: review.Path, Files: make(map[string]model.FileVersion, len(tree))}
	plan, err := e.checkoutPlan(ctx, review.Commit, names)
	if err != nil {
		return inventory, err
	}
	for _, name := range names {
		entry := tree[name]
		version, body, versionErr := entryVersionFrom(reader, entry, plan.rule(name))
		if versionErr != nil {
			return inventory, versionErr
		}
		if version.Kind == model.Symlink {
			if err = validateLink(name, string(body)); err != nil {
				return inventory, err
			}
		}
		inventory.Files[name] = version
	}
	return inventory, nil
}

func (e *Engine) finishBatchReview(ctx context.Context, receipt batchReceipt, reader *git.Reader, tree map[string]git.TreeEntry, names []string) error {
	inventory, err := e.batchInventory(ctx, receipt.BatchReview, reader, tree, names)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(receipt.Path); os.IsNotExist(err) {
		_, err = e.Repository.Run(ctx, e.Repository.Root, nil, "worktree", "add", "--detach", "--no-checkout", receipt.Path, receipt.Commit)
	}
	if err != nil {
		return err
	}
	if err = e.verifyReviewGit(ctx, inventory, receipt.Commit); err != nil {
		return err
	}
	root, err := fsops.OpenRoot(receipt.Path)
	if err != nil {
		return err
	}
	defer root.Close()
	present, err := reviewFilesOnDisk(root, inventory, "Unexpected file in interrupted batch review: ")
	if err != nil {
		return err
	}
	respect, err := e.batchFileMode(ctx, receipt.ID)
	if err != nil {
		return err
	}
	if err = compareReviewFiles(ctx, root, inventory, present, respect, "Edited file in interrupted batch review: "); err != nil {
		return err
	}
	existing := map[string]bool{}
	for _, path := range present {
		existing[path] = true
	}
	missing := []string{}
	for _, name := range names {
		if !existing[name] {
			missing = append(missing, name)
		}
	}
	if err = e.materializeReview(ctx, root, reader, nil, inventory, missing); err != nil {
		return err
	}
	if err = e.verifyReviewFiles(inventory, respect); err != nil {
		return err
	}
	if err = e.verifyBatchIndex(ctx, receipt, true); err != nil {
		return err
	}
	_, err = e.Repository.Run(ctx, receipt.Path, nil, "read-tree", receipt.Commit)
	return err
}

func (e *Engine) batchFileMode(ctx context.Context, id string) (bool, error) {
	if runtime.GOOS == "windows" {
		return false, nil
	}
	config, err := e.Repository.CheckoutConfig(ctx)
	if err != nil || !config.FileMode {
		return false, err
	}
	root, err := fsops.OpenRoot(e.WorkRoot)
	if err != nil {
		return false, err
	}
	defer root.Close()
	faithful, known := root.ExecutableBitsFaithful(batchPath(id))
	return !known || faithful, nil
}

func (e *Engine) verifyBatchIndex(ctx context.Context, receipt batchReceipt, allowMissing bool) error {
	repo := e.Repository
	if allowMissing {
		body, err := repo.Run(ctx, receipt.Path, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
		if err != nil {
			return err
		}
		if _, err = os.Lstat(strings.TrimSpace(string(body))); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
	}
	if _, err := repo.Run(ctx, receipt.Path, nil, "diff-index", "--cached", "--quiet", receipt.Commit, "--"); err != nil {
		return model.Fail(3, "batch-review-index-changed", "Review index differs from the candidate commit")
	}
	return nil
}

func saveBatch(root *fsops.Root, receipt batchReceipt) error {
	body, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err = root.MkdirAll(".workcopy-batches", 0700); err != nil {
		return err
	}
	return root.WriteAtomic(batchPath(receipt.ID), body, 0600)
}

func (e *Engine) loadBatch(root *fsops.Root, id string) (batchReceipt, error) {
	var receipt batchReceipt
	if !model.ValidID(id) {
		return receipt, model.Fail(2, "invalid-batch-id", "Use the batch ID from the combined review")
	}
	body, err := root.ReadFileLimit(batchPath(id), model.MaxMetadataBytes)
	if err != nil {
		return receipt, err
	}
	if err = model.DecodeStrict(body, &receipt); err != nil {
		return receipt, err
	}
	if receipt.ID != id || receipt.Repository != e.Repository.CommonDir || receipt.Checkout != e.Repository.Root ||
		receipt.Path != filepath.Join(e.WorkRoot, ".workcopy-reviews", "batch-"+id) ||
		!strings.HasPrefix(receipt.Target, "refs/heads/") || !git.ValidObjectID(receipt.Base) || !git.ValidObjectID(receipt.Commit) ||
		len(receipt.Lanes) == 0 || len(receipt.Lanes) != len(receipt.Inputs) {
		return receipt, model.Fail(3, "batch-ownership-mismatch", "Batch receipt does not match this checkout and workspace")
	}
	return receipt, nil
}

// Verify that a candidate still has exactly the approved parent.
func (e *Engine) verifyBatchParent(ctx context.Context, receipt batchReceipt) error {
	body, err := e.Repository.Run(ctx, e.Repository.Root, nil, "rev-list", "--parents", "-n", "1", receipt.Commit)
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(body), []byte(receipt.Commit+" "+receipt.Base)) {
		return model.Fail(3, "invalid-batch-parent", "Candidate is not a direct child of the reviewed base")
	}
	return nil
}

// RecoverBatch resumes review preparation or records a landing confirmed by
// the branch value. It never initiates a checkout mutation.
func (e *Engine) RecoverBatch(ctx context.Context, id string) (Report, error) {
	report := Report{Command: "batch-recover"}
	root, registry, err := e.rootLock()
	if err != nil {
		return report, err
	}
	defer root.Close()
	defer registry.Close()
	receipt, err := e.loadBatch(root, id)
	if err != nil {
		return report, err
	}
	report.Batch = &receipt.BatchReview
	switch receipt.State {
	case "preparing":
		if err = e.verifyBatchParent(ctx, receipt); err != nil {
			return report, err
		}
		tree, treeErr := e.Repository.Tree(ctx, receipt.Commit)
		if treeErr != nil {
			return report, treeErr
		}
		names, treeErr := validateTree(tree, model.Selection{All: true}, false)
		if treeErr != nil {
			return report, treeErr
		}
		reader, readerErr := e.Repository.Reader(ctx)
		if readerErr != nil {
			return report, readerErr
		}
		defer reader.Close()
		if err = e.finishBatchReview(ctx, receipt, reader, tree, names); err != nil {
			return report, err
		}
		receipt.State = "prepared"
	case "landing":
		ref, current, targetErr := e.batchTarget(ctx, receipt.Target)
		if targetErr != nil || ref != receipt.Target || current != receipt.Commit {
			return report, model.Fail(3, "batch-landing-interrupted", "Landing was interrupted. Inspect the target HEAD, index and files before preparing another batch.")
		}
		receipt.State = "landed"
	default:
		report.ExitCode, report.Message = 4, "No recovery required for this batch."
		return report, nil
	}
	if err = saveBatch(root, receipt); err != nil {
		return report, err
	}
	report.Message = "Batch recovery completed."
	return report, nil
}
