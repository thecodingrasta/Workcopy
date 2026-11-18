package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"

	"workcopy/internal/git"
	"workcopy/internal/model"
)

// LandBatch advances one checked out branch to the exact reviewed commit.
// A persisted landing intent lets a retry distinguish completed work from an
// interrupted attempt. An interrupted attempt is never replayed automatically.
func (e *Engine) LandBatch(ctx context.Context, id string, approve, dryRun bool) (Report, error) {
	report := Report{Command: "batch-land"}
	if !approve {
		return report, model.Fail(2, "batch-approval-required", "Review the combined result and pass --approve with its batch ID")
	}
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
	if receipt.State == "landed" {
		report.Message = "This batch was already landed."
		return report, nil
	}
	repo := e.Repository
	ref, current, err := e.batchTarget(ctx, receipt.Target)
	if err != nil || ref != receipt.Target {
		return report, model.Fail(3, "batch-target-changed", "The reviewed local target branch is unavailable")
	}
	if receipt.State == "landing" {
		if current == receipt.Commit {
			if !dryRun {
				receipt.State = "landed"
				if err = saveBatch(root, receipt); err != nil {
					return report, err
				}
			}
			report.Message = "Git already landed the approved batch."
			return report, nil
		}
		return report, model.Fail(3, "batch-landing-interrupted", "Landing was interrupted. Inspect the target HEAD, index and files before preparing another batch.")
	}
	if receipt.State != "prepared" || current != receipt.Base {
		return report, model.Fail(3, "stale-batch", "The target changed or the review is incomplete. Prepare and review a fresh batch.")
	}
	if err = e.verifyBatchParent(ctx, receipt); err != nil {
		return report, err
	}
	opened, closeInputs, err := e.batchInputs(receipt.Lanes)
	if err != nil {
		return report, err
	}
	defer closeInputs()
	for i, lane := range opened {
		input, _, inspectErr := e.inspectBatchInput(ctx, lane)
		if inspectErr != nil {
			return report, inspectErr
		}
		if input != receipt.Inputs[i] {
			return report, model.Fail(3, "stale-batch-input", "Lane changed after batch preparation: "+lane.record.Name)
		}
	}
	entries, err := repo.Tree(ctx, receipt.Commit)
	if err != nil {
		return report, err
	}
	names, err := validateTree(entries, model.Selection{All: true}, false)
	if err != nil {
		return report, err
	}
	reader, err := repo.Reader(ctx)
	if err != nil {
		return report, err
	}
	defer reader.Close()
	inventory, err := e.batchInventory(ctx, receipt.BatchReview, reader, entries, names)
	if err != nil {
		return report, err
	}
	if err = e.verifyReviewGit(ctx, inventory, receipt.Commit); err != nil {
		return report, err
	}
	if err = e.verifyBatchIndex(ctx, receipt, false); err != nil {
		return report, err
	}
	respect, err := e.batchFileMode(ctx, receipt.ID)
	if err != nil {
		return report, err
	}
	if err = e.verifyReviewFiles(inventory, respect); err != nil {
		return report, err
	}
	if err = e.batchCheckoutPreflight(ctx, receipt); err != nil {
		return report, err
	}
	checkout, err := repo.CheckoutConfig(ctx)
	if err != nil {
		return report, err
	}
	config := []string{"-c", "core.autocrlf=" + checkout.AutoCRLF, "-c", "core.eol=" + checkout.EOL}
	if _, err = repo.Run(ctx, repo.Root, nil, append(append([]string{}, config...), "read-tree", "-n", "-m", "-u", receipt.Base, receipt.Commit)...); err != nil {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		return report, model.Fail(3, "batch-checkout-preflight", "Git blocked batch landing: "+err.Error())
	}
	if dryRun {
		report.Message = "Batch approval and checkout preflight passed."
		return report, nil
	}
	err = repo.UpdateCheckedOutBranch(ctx, receipt.Target, receipt.Base, receipt.Commit, func() error {
		if policyErr := e.checkBatchSigningPolicy(ctx); policyErr != nil {
			return policyErr
		}
		// Persist intent immediately before the operation that can write the
		// checkout. A refusal during ref preparation leaves the receipt usable.
		next := receipt
		next.State = "landing"
		if saveErr := saveBatch(root, next); saveErr != nil {
			return saveErr
		}
		receipt.State = next.State
		// Use Git's two-tree checkout and index checks. Do not use reset,
		// autostash, or a merge that follows HEAD in a separate process.
		_, writeErr := repo.Run(ctx, repo.Root, nil, append(config, "read-tree", "-m", "-u", receipt.Base, receipt.Commit)...)
		return writeErr
	})
	if err != nil {
		return report, fmt.Errorf("batch landing stopped: %w", err)
	}
	receipt.State = "landed"
	if err = saveBatch(root, receipt); err != nil {
		return report, err
	}
	report.Message = "Approved batch landed on " + receipt.Target + "."
	return report, nil
}

func (e *Engine) batchCheckoutPreflight(ctx context.Context, receipt batchReceipt) error {
	repo := e.Repository
	if err := e.verifyBatchCheckoutHead(ctx, receipt); err != nil {
		return err
	}
	if err := e.checkBatchSigningPolicy(ctx); err != nil {
		return err
	}
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-apply", "rebase-merge", "sequencer"} {
		path, pathErr := repo.Run(ctx, repo.Root, nil, "rev-parse", "--path-format=absolute", "--git-path", marker)
		if pathErr != nil {
			return pathErr
		}
		if _, statErr := os.Lstat(strings.TrimSpace(string(path))); statErr == nil {
			return model.Fail(3, "batch-git-operation-active", "Finish the current Git operation before landing the batch: "+marker)
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
	}
	return e.batchCheckLiveAttributes(ctx, receipt, repo)
}

func (e *Engine) checkBatchSigningPolicy(ctx context.Context) error {
	repo := e.Repository
	body, err := repo.Run(ctx, repo.Root, nil, "config", "--type=bool", "--default=false", "--get", "merge.verifySignatures")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(body)) == "true" {
		return model.Fail(3, "batch-signature-required", "This repository requires verified merge signatures. The batch candidate is unsigned. The review is still prepared.")
	}
	return nil
}

func (e *Engine) verifyBatchCheckoutHead(ctx context.Context, receipt batchReceipt) error {
	repo := e.Repository
	branch, err := repo.Run(ctx, repo.Root, nil, "symbolic-ref", "-q", "HEAD")
	if err != nil || strings.TrimSpace(string(branch)) != receipt.Target {
		return model.Fail(3, "batch-target-not-checked-out", "Land from the checkout of the reviewed target branch")
	}
	head, err := repo.Run(ctx, repo.Root, nil, "rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != receipt.Base {
		return model.Fail(3, "stale-batch", "Target changed. Prepare and review a fresh batch.")
	}
	return nil
}

func (e *Engine) batchCheckLiveAttributes(ctx context.Context, receipt batchReceipt, repo *git.Repository) error {
	cached, err := repo.Run(ctx, repo.Root, nil, "ls-files", "-z", "--cached")
	if err != nil {
		return err
	}
	changed, err := repo.Run(ctx, repo.Root, nil, "diff-tree", "-r", "--no-commit-id", "--no-renames", "--name-only", "-z", receipt.Base, receipt.Commit, "--")
	if err != nil {
		return err
	}
	attrs, err := repo.Run(ctx, repo.Root, append(cached, changed...), "check-attr", "-z", "--stdin", "filter", "working-tree-encoding", "ident")
	if err != nil {
		return err
	}
	fields := bytes.Split(attrs, []byte{0})
	for i := 0; i+2 < len(fields); i += 3 {
		if value := string(fields[i+2]); value != "unspecified" && value != "unset" {
			return model.Fail(3, "batch-unsupported-attribute", fmt.Sprintf("Checkout path %s uses unsupported attribute %s", fields[i], fields[i+1]))
		}
	}
	// read-tree's dry-run does not catch every ignored-file obstruction that
	// merge will reject. Include ignored files without relying on Git config
	// or diagnostic text, so a normal collision leaves the receipt reusable.
	others, err := repo.Run(ctx, repo.Root, nil, "ls-files", "-z", "--others")
	if err != nil {
		return err
	}
	paths := strings.Split(strings.TrimSuffix(string(changed), "\x00"), "\x00")
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		for i, path := range paths {
			paths[i] = strings.ToLower(path)
		}
	}
	sort.Strings(paths)
	for _, path := range strings.Split(strings.TrimSuffix(string(others), "\x00"), "\x00") {
		if path == "" {
			continue
		}
		key := strings.TrimSuffix(path, "/")
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			key = strings.ToLower(key)
		}
		if i := sort.SearchStrings(paths, key); i < len(paths) && paths[i] == key {
			return model.Fail(3, "batch-checkout-collision", "A local checkout file blocks batch landing: "+path)
		}
		if batchPathHasPrefix(paths, key+"/") {
			return model.Fail(3, "batch-checkout-collision", "A local checkout path blocks batch landing: "+path)
		}
		for slash := strings.LastIndexByte(key, '/'); slash >= 0; slash = strings.LastIndexByte(key[:slash], '/') {
			parent := key[:slash]
			if i := sort.SearchStrings(paths, parent); i < len(paths) && paths[i] == parent {
				return model.Fail(3, "batch-checkout-collision", "A local checkout path blocks batch landing: "+path)
			}
		}
	}
	return nil
}

func batchPathHasPrefix(paths []string, prefix string) bool {
	i := sort.SearchStrings(paths, prefix)
	return i < len(paths) && strings.HasPrefix(paths[i], prefix)
}
