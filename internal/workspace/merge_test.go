package workspace

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitAt(t *testing.T, directory string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", directory}, args...)...)
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}
func TestReviewAndExplicitCommitPreserveDirtyMainCheckout(t *testing.T) {
	r, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "b.txt"), []byte("agent contribution\n"), 0644)
	r.Write("a.txt", "human staged edit\n")
	r.Git("add", "a.txt")
	r.Write("untracked.txt", "human local file\n")
	head := r.Git("rev-parse", "HEAD")
	symbolic := r.Git("symbolic-ref", "HEAD")
	indexBefore, _ := os.ReadFile(filepath.Join(r.Path, ".git", "index"))
	review, err := e.Merge(context.Background(), "task", MergeOptions{}, false)
	if err != nil || review.Review == nil {
		t.Fatal(review, err)
	}
	if gitAt(t, review.Review.Path, "rev-parse", "HEAD") != head {
		t.Fatal("default merge created commit")
	}
	if gitAt(t, review.Review.Path, "diff", "--cached", "--name-only") != "" {
		t.Fatal("review staged changes")
	}
	if !strings.Contains(gitAt(t, review.Review.Path, "diff", "--name-only"), "b.txt") {
		t.Fatal("contribution missing")
	}
	committed, err := e.Merge(context.Background(), "task", MergeOptions{Commit: true, Approve: true, ReviewID: review.Review.ID, Message: "Reviewed contribution"}, false)
	if err != nil || committed.Review == nil || committed.Review.State != "committed" {
		t.Fatal(committed, err)
	}
	if gitAt(t, review.Review.Path, "status", "--porcelain") != "" {
		t.Fatal("committed review is not clean")
	}
	indexAfter, _ := os.ReadFile(filepath.Join(r.Path, ".git", "index"))
	if !bytes.Equal(indexBefore, indexAfter) || r.Git("rev-parse", "HEAD") != head || r.Git("symbolic-ref", "HEAD") != symbolic {
		t.Fatal("main HEAD/index changed")
	}
	b, _ := os.ReadFile(filepath.Join(r.Path, "a.txt"))
	if string(b) != "human staged edit\n" {
		t.Fatal("human edit changed")
	}
}
func TestMergeReconcilesUpstreamBeforePreparingReview(t *testing.T) {
	r, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "a.txt"), []byte("LOCAL\nsecond\nthird\nfourth\nfifth\n"), 0644)
	r.Write("a.txt", "first\nsecond\nthird\nfourth\nUPSTREAM\n")
	r.Commit("upstream")
	report, err := e.Merge(context.Background(), "task", MergeOptions{}, false)
	if err != nil || report.Review == nil {
		t.Fatal(report, err)
	}
	b, _ := os.ReadFile(filepath.Join(report.Review.Path, "a.txt"))
	if string(b) != "LOCAL\nsecond\nthird\nfourth\nUPSTREAM\n" {
		t.Fatal("review reverted upstream", string(b))
	}
}
func TestCommitRejectsChangedReviewAndMissingApproval(t *testing.T) {
	_, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "b.txt"), []byte("local\n"), 0644)
	report, err := e.Merge(context.Background(), "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Merge(context.Background(), "task", MergeOptions{Commit: true, ReviewID: report.Review.ID, Message: "test"}, false); err == nil {
		t.Fatal("approval not required")
	}
	os.WriteFile(filepath.Join(report.Review.Path, "b.txt"), []byte("later review edit\n"), 0644)
	if _, err = e.Merge(context.Background(), "task", MergeOptions{Commit: true, Approve: true, ReviewID: report.Review.ID, Message: "test"}, false); err == nil {
		t.Fatal("changed review silently approved")
	}
}

func TestMergeDoesNotReuseAnEditedReview(t *testing.T) {
	_, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "b.txt"), []byte("local\n"), 0644)
	first, err := e.Merge(context.Background(), "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(first.Review.Path, "b.txt"), []byte("human review edit\n"), 0644)
	repeated, err := e.Merge(context.Background(), "task", MergeOptions{}, false)
	if err == nil && repeated.Review != nil && repeated.Review.ID == first.Review.ID {
		t.Fatal("edited review reported as unchanged")
	}
	body, _ := os.ReadFile(filepath.Join(first.Review.Path, "b.txt"))
	if string(body) != "human review edit\n" {
		t.Fatal("review edit was overwritten")
	}
}

func TestCommitRecoveryPreservesNewStagedChanges(t *testing.T) {
	_, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "b.txt"), []byte("local\n"), 0644)
	report, err := e.Merge(context.Background(), "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	receipt := *report.Review
	tree := gitAt(t, receipt.Path, "rev-parse", "HEAD^{tree}")
	receipt.Commit = gitAt(t, receipt.Path, "commit-tree", tree, "-p", receipt.BaseCommit, "-m", "fixture pending")
	receipt.State = "committing"
	opened, err := e.openLane("task")
	if err != nil {
		t.Fatal(err)
	}
	defer opened.close()
	if _, err = replaceReview(opened.store, receipt); err != nil {
		t.Fatal(err)
	}
	gitAt(t, receipt.Path, "read-tree", "--empty")
	indexTree := gitAt(t, receipt.Path, "write-tree")
	if err = e.finishCommit(context.Background(), opened.store, receipt); err == nil {
		t.Fatal("recovery replaced later staged changes")
	}
	if got := gitAt(t, receipt.Path, "write-tree"); got != indexTree {
		t.Fatal("index changed during rejected recovery")
	}
	if got := gitAt(t, receipt.Path, "rev-parse", "HEAD"); got != receipt.BaseCommit {
		t.Fatal("HEAD advanced during rejected recovery")
	}
}

func TestPreparationRecoveryPreservesNewStagedChanges(t *testing.T) {
	_, e, lane := fixtureLane(t)
	if err := os.WriteFile(filepath.Join(lane, "b.txt"), []byte("local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := e.Merge(context.Background(), "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	receipt := *report.Review
	opened, err := e.openLane("task")
	if err != nil {
		t.Fatal(err)
	}
	defer opened.close()
	receipt.State = "preparing"
	if _, err = replaceReview(opened.store, receipt); err != nil {
		t.Fatal(err)
	}
	gitAt(t, receipt.Path, "add", "b.txt")
	indexTree := gitAt(t, receipt.Path, "write-tree")
	if _, err = e.Recover(context.Background(), "task", false); err == nil {
		t.Fatal("recovery discarded staging added after review preparation")
	}
	if got := gitAt(t, receipt.Path, "write-tree"); got != indexTree {
		t.Fatal("recovery changed staged contribution")
	}
}
