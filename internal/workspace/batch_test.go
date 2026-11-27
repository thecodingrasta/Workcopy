package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"workcopy/internal/model"
)

func TestBatchRefTransactionRejectsSameBaseBranchSwitch(t *testing.T) {
	r, engine, lane := fixtureLane(t)
	ctx := t.Context()
	if err := os.WriteFile(filepath.Join(lane, "b.txt"), []byte("selected contribution\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := engine.PrepareBatch(ctx, []string{"task"}, "main", false)
	if err != nil || report.Batch == nil {
		t.Fatal(report, err)
	}
	batch := report.Batch
	if err = engine.verifyBatchCheckoutHead(ctx, batchReceipt{BatchReview: *batch}); err != nil {
		t.Fatal(err)
	}
	// Switch after validation but before the operation acquires Git's locks.
	r.Git("switch", "-c", "other")
	indexPath := filepath.Join(r.Path, ".git", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	wrote := false
	err = engine.Repository.UpdateCheckedOutBranch(ctx, batch.Target, batch.Base, batch.Commit, func() error {
		wrote = true
		return nil
	})
	if err == nil || wrote {
		t.Fatalf("allowed wrong-branch checkout write: wrote=%t err=%v", wrote, err)
	}
	indexAfter, err := os.ReadFile(indexPath)
	if err != nil || !bytes.Equal(indexBefore, indexAfter) {
		t.Fatalf("index changed on refusal: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(r.Path, "b.txt"))
	if err != nil || string(content) != "base\n" {
		t.Fatalf("checkout changed on refusal: %q %v", content, err)
	}
	if r.Git("rev-parse", "main") != batch.Base || r.Git("rev-parse", "other") != batch.Base || r.Git("symbolic-ref", "HEAD") != "refs/heads/other" {
		t.Fatal("branch changed on refusal")
	}
	r.Git("switch", "main") // Aborting must release the prepared locks.
}

func TestBatchRefTransactionKeepsHeadAndBaseLocked(t *testing.T) {
	r, engine, lane := fixtureLane(t)
	ctx := t.Context()
	if err := os.WriteFile(filepath.Join(lane, "b.txt"), []byte("selected contribution\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := engine.PrepareBatch(ctx, []string{"task"}, "main", false)
	if err != nil || report.Batch == nil {
		t.Fatal(report, err)
	}
	batch := report.Batch
	r.Git("branch", "other")
	abort := errors.New("abort fixture transaction")
	err = engine.Repository.UpdateCheckedOutBranch(ctx, batch.Target, batch.Base, batch.Commit, func() error {
		repo := engine.Repository
		if _, err := repo.Run(ctx, r.Path, nil, "symbolic-ref", "HEAD", "refs/heads/other"); err == nil {
			t.Error("Git allowed a HEAD switch during a prepared transaction")
		}
		if _, err := repo.Run(ctx, r.Path, nil, "update-ref", "refs/heads/main", batch.Commit, batch.Base); err == nil {
			t.Error("Git allowed the approved target to advance during a prepared transaction")
		}
		return abort
	})
	if !errors.Is(err, abort) || r.Git("rev-parse", "main") != batch.Base || r.Git("symbolic-ref", "HEAD") != batch.Target {
		t.Fatalf("transaction did not abort without updating refs: %v", err)
	}
}

func TestBatchSignaturePolicyRefusalRetainsPreparedReceipt(t *testing.T) {
	r, engine, lane := fixtureLane(t)
	ctx := t.Context()
	if err := os.WriteFile(filepath.Join(lane, "b.txt"), []byte("selected contribution\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := engine.PrepareBatch(ctx, []string{"task"}, "main", false)
	if err != nil || prepared.Batch == nil {
		t.Fatal(prepared, err)
	}
	r.Git("config", "--local", "merge.verifySignatures", "true")
	batch := prepared.Batch
	indexPath := filepath.Join(r.Path, ".git", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := engine.LandBatch(ctx, batch.ID, true, false)
	var failure *model.Failure
	if !errors.As(err, &failure) || failure.Code != "batch-signature-required" || blocked.Batch == nil || blocked.Batch.State != "prepared" {
		t.Fatalf("unexpected signature refusal: %+v %v", blocked, err)
	}
	indexAfter, err := os.ReadFile(indexPath)
	if err != nil || !bytes.Equal(indexBefore, indexAfter) || r.Git("rev-parse", "HEAD") != batch.Base {
		t.Fatal("signature refusal changed the index or HEAD")
	}
	if got, err := os.ReadFile(filepath.Join(r.Path, "b.txt")); err != nil || string(got) != "base\n" {
		t.Fatalf("signature refusal changed file: %q %v", got, err)
	}
	if r.Git("config", "--get", "merge.verifySignatures") != "true" {
		t.Fatal("signing policy changed")
	}
	r.Git("config", "--local", "--unset", "merge.verifySignatures")
	landed, err := engine.LandBatch(ctx, batch.ID, true, false)
	if err != nil || landed.Batch == nil || landed.Batch.State != "landed" {
		t.Fatal(landed, err)
	}
}

func TestBatchAdmissionRejectsTargetThatAdvancesBeforeAllocation(t *testing.T) {
	r, engine, lane := fixtureLane(t)
	ctx := t.Context()
	if err := os.WriteFile(filepath.Join(lane, "b.txt"), []byte("selected contribution\n"), 0600); err != nil {
		t.Fatal(err)
	}
	admissions := 0
	report, err := engine.PrepareBatchWithAdmission(ctx, []string{"task"}, "main", nil, false, func(plan Plan) error {
		admissions++
		r.Write("new-target-file", "larger target than admitted\n")
		r.Commit("advance during admission")
		return nil
	})
	var failure *model.Failure
	if admissions != 1 || !errors.As(err, &failure) || failure.Code != "batch-target-changed" || report.Batch != nil {
		t.Fatalf("unadmitted review: %+v, admissions=%d, err=%v", report, admissions, err)
	}
	for _, dir := range []string{".workcopy-reviews", ".workcopy-batches"} {
		if _, statErr := os.Stat(filepath.Join(engine.WorkRoot, dir)); !os.IsNotExist(statErr) {
			t.Fatalf("review created before admission was valid: %s: %v", dir, statErr)
		}
	}
}

func TestBatchCombinesIndependentLaneAndUpstreamEditsToSameFile(t *testing.T) {
	r, engine, lane := fixtureLane(t)
	if err := os.WriteFile(filepath.Join(lane, "a.txt"), []byte("LANE\nsecond\nthird\nfourth\nfifth\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r.Write("a.txt", "first\nsecond\nthird\nfourth\nTARGET\n")
	r.Commit("advance target on the same path")
	report, err := engine.PrepareBatch(t.Context(), []string{"task"}, "main", false)
	if err != nil || report.Batch == nil {
		t.Fatal(report, err)
	}
	if got := r.Git("show", report.Batch.Commit+":a.txt"); got != "LANE\nsecond\nthird\nfourth\nTARGET" {
		t.Fatalf("candidate lost a change: %q", got)
	}
	if r.Git("rev-parse", "HEAD") != report.Batch.Base {
		t.Fatal("preparation advanced the branch")
	}
}

func TestBatchPreservesStreamedFileContributions(t *testing.T) {
	defer model.SetInlineFileBytes(smallThreshold)()
	r, _ := largeRepository(t)
	ctx := t.Context()
	engine, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := engine.Create(ctx, "task", CreateOptions{AllowTempRoot: true, All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	const path = "assets/model.bin"
	body := pseudo(3*smallThreshold+17, 41)
	if err := os.WriteFile(filepath.Join(created.Workspace.Path, filepath.FromSlash(path)), body, 0600); err != nil {
		t.Fatal(err)
	}
	r.Write(path, string(pseudo(2*smallThreshold, 42)))
	r.Commit("upstream large-file edit")
	conflicted, err := engine.PrepareBatch(ctx, []string{"task"}, "main", false)
	if conflicted.BatchConflict == nil {
		t.Fatalf("expected a large-file conflict, got %+v: %v", conflicted, err)
	}
	review, err := engine.PrepareBatchWithOptions(ctx, []string{"task"}, "main", []BatchResolution{{Token: conflicted.BatchConflict.Token, Choice: "lane"}}, true, false, nil)
	if err != nil || review.Batch == nil {
		t.Fatalf("prepare large-file review: %+v: %v", review, err)
	}
	if got := digest(laneFile(t, review.Batch.Path, path)); got != digest(body) {
		t.Fatalf("review contains incorrect large-file bytes: %s", got)
	}
	if _, err := engine.LandBatch(ctx, review.Batch.ID, true, false); err != nil {
		t.Fatal(err)
	}
	if got := digest(laneFile(t, r.Path, path)); got != digest(body) {
		t.Fatalf("landing contains incorrect large-file bytes: %s", got)
	}
}

func TestWorktreeFreeBatchReviewAndLand(t *testing.T) {
	r, engine, lane := fixtureLane(t)
	ctx := t.Context()
	if err := os.WriteFile(filepath.Join(lane, "b.txt"), []byte("worktree-free batch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	beforeWorktrees := r.Git("worktree", "list", "--porcelain")
	prepared, err := engine.PrepareBatch(ctx, []string{"task"}, "main", false)
	if err != nil || prepared.Batch == nil {
		t.Fatalf("prepare worktree-free batch: %+v: %v", prepared, err)
	}
	if prepared.Batch.Path != "" || prepared.Batch.Worktree {
		t.Fatalf("default batch review should not create a worktree: %+v", prepared.Batch)
	}
	if afterWorktrees := r.Git("worktree", "list", "--porcelain"); afterWorktrees != beforeWorktrees {
		t.Fatalf("default batch review created a git worktree:\n%s", afterWorktrees)
	}
	if _, err := engine.LandBatch(ctx, prepared.Batch.ID, true, false); err != nil {
		t.Fatalf("land worktree-free batch: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(r.Path, "b.txt"))
	if err != nil || string(got) != "worktree-free batch\n" {
		t.Fatalf("landed content mismatch: %q %v", got, err)
	}
}

func TestBatchRejectsStaleLaneAndTarget(t *testing.T) {
	r, engine, lane := fixtureLane(t)
	ctx := context.Background()
	edit := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(lane, "b.txt"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	prepare := func() *BatchReview {
		t.Helper()
		report, err := engine.PrepareBatch(ctx, []string{"task"}, "main", false)
		if err != nil || report.Batch == nil {
			t.Fatal(report, err)
		}
		return report.Batch
	}
	assertRefusal := func(id, code string) {
		t.Helper()
		_, err := engine.LandBatch(ctx, id, true, false)
		var failure *model.Failure
		if !errors.As(err, &failure) || failure.Code != code {
			t.Fatalf("want %s, got %v", code, err)
		}
	}
	edit("lane contribution\n")
	batch := prepare()
	edit("new contribution after review\n")
	assertRefusal(batch.ID, "stale-batch-input")
	if r.Git("rev-parse", "HEAD") != batch.Base {
		t.Fatal("stale lane changed the branch")
	}
	batch = prepare()
	r.Write("advanced.txt", "new upstream work\n")
	r.Commit("advance target")
	target := r.Git("rev-parse", "HEAD")
	assertRefusal(batch.ID, "stale-batch")
	if r.Git("rev-parse", "HEAD") != target {
		t.Fatal("stale target was overwritten")
	}
}

func TestBatchRecoveryDoesNotReplayCheckoutMutation(t *testing.T) {
	r, engine, lane := fixtureLane(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(lane, "b.txt"), []byte("approved content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := engine.PrepareBatch(ctx, []string{"task"}, "", false)
	if err != nil || prepared.Batch == nil {
		t.Fatal(prepared, err)
	}
	root, lock, err := engine.rootLock()
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := engine.loadBatch(root, prepared.Batch.ID)
	if err == nil {
		receipt.State = "landing"
		err = saveBatch(root, receipt)
	}
	lock.Close()
	root.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RecoverBatch(ctx, receipt.ID); err == nil || r.Git("rev-parse", "HEAD") != receipt.Base {
		t.Fatal("recovery retried or accepted an unconfirmed landing")
	}
	r.Git("merge", "--ff-only", receipt.Commit)
	recovered, err := engine.RecoverBatch(ctx, receipt.ID)
	if err != nil || recovered.Batch == nil || recovered.Batch.State != "landed" {
		t.Fatal(recovered, err)
	}
}
