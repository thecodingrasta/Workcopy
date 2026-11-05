package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/testutil"
)

func wideRepository(t *testing.T, files int) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	for n := 0; n < files; n++ {
		r.Write(fmt.Sprintf("src/p%02d/dir%d/file%04d.txt", n%7, n%3, n), fmt.Sprintf("content %d\n", n))
	}
	r.Commit("wide repository")
	return r
}

// cancelAfter returns a context that is cancelled by the first progress report
// at or beyond the given file count, as an MCP deadline would interrupt a long
// operation midway.
func cancelAfter(done int) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	return WithProgress(ctx, func(_ string, completed, _ int) {
		if completed >= done {
			cancel()
		}
	}), cancel
}

// WC-011: the MCP deadline can interrupt review preparation. The lane must say
// so, refuse further work, and finish the same review on recover.
func TestInterruptedReviewPreparationRecoversToTheSameReview(t *testing.T) {
	t.Parallel()
	r := wideRepository(t, 520)
	background := context.Background()
	e, err := New(background, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(background, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	if err = os.WriteFile(filepath.Join(lane, "src", "p00", "dir0", "file0000.txt"), []byte("edited\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := cancelAfter(256)
	defer cancel()
	if _, err = e.Merge(ctx, "task", MergeOptions{}, false); err == nil {
		t.Fatal("the interrupted review must report failure")
	}
	if _, err = e.Status(background, "task"); failureCode(t, err) != "recovery-required" {
		t.Fatalf("an unfinished review must block the lane with recovery-required: %v", err)
	}
	if _, err = e.Merge(background, "task", MergeOptions{}, false); failureCode(t, err) != "recovery-required" {
		t.Fatalf("a second review must not start over the unfinished one: %v", err)
	}

	recovered, err := e.Recover(background, "task", false)
	if err != nil || !strings.Contains(recovered.Message, "review preparation recovered") {
		t.Fatalf("recover must finish the review: %+v %v", recovered, err)
	}
	reused, err := e.Merge(background, "task", MergeOptions{}, false)
	if err != nil || reused.Review == nil || reused.Review.State != "prepared" || !strings.Contains(reused.Message, "reused") {
		t.Fatalf("the recovered review must be the prepared one: %+v %v", reused, err)
	}
	porcelain := strings.TrimSpace(r.GitAt(reused.Review.Path, "", "status", "--porcelain"))
	if strings.TrimSpace(porcelain) != "M src/p00/dir0/file0000.txt" {
		t.Fatalf("the recovered review must contain exactly the real change, got %q", porcelain)
	}
}

// Creation interrupted midway leaves a registered, recoverable lane that
// recover completes with the full source and nothing else.
func TestInterruptedCreationRecoversToACleanLane(t *testing.T) {
	t.Parallel()
	r := wideRepository(t, 520)
	background := context.Background()
	e, err := New(background, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := cancelAfter(256)
	defer cancel()
	if _, err = e.Create(ctx, "task", CreateOptions{All: true}, false); err == nil {
		t.Fatal("the interrupted creation must report failure")
	}
	listed, err := e.List(background)
	if err != nil || len(listed.Workspaces) != 1 || listed.Workspaces[0].Phase != "creating" {
		t.Fatalf("an interrupted creation must stay visible as creating: %+v %v", listed.Workspaces, err)
	}
	if _, err = e.Status(background, "task"); failureCode(t, err) != "recovery-required" {
		t.Fatalf("status must send the user to recover: %v", err)
	}
	if _, err = e.Recover(background, "task", false); err != nil {
		t.Fatal(err)
	}
	status, err := e.Status(background, "task")
	if err != nil || len(status.Changes) != 0 {
		t.Fatalf("recovered lane must be clean: %+v %v", status.Changes, err)
	}
	count := 0
	err = filepath.WalkDir(filepath.Join(e.WorkRoot, "task", "src"), func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() {
			count++
		}
		return walkErr
	})
	if err != nil || count != 520 {
		t.Fatalf("recovered lane has %d of 520 files: %v", count, err)
	}
}

// The pool must give the same result as strictly sequential operation.
func TestBulkOperationsAreIdenticalWithOneWorker(t *testing.T) {
	t.Setenv("WORKCOPY_IO_WORKERS", "1")
	r := wideRepository(t, 48)
	ctx := context.Background()
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(created.Workspace.Path, "src", "p01", "dir1", "file0001.txt"), []byte("edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	review, err := e.Merge(ctx, "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(r.GitAt(review.Review.Path, "", "status", "--porcelain")); got != "M src/p01/dir1/file0001.txt" {
		t.Fatalf("sequential mode produced a different review: %q", got)
	}
}
