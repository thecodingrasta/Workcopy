package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/model"
	"workcopy/internal/testutil"
)

func failureCode(t *testing.T, err error) string {
	t.Helper()
	var failure *model.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("expected a Workcopy failure, got %v", err)
	}
	return failure.Code
}

// WC-015: a lane directory deleted outside Workcopy must be reported as
// missing everywhere and be clearable without touching anything else.
func TestMissingLaneIsReportedAndClearedWithoutTouchingNeighbours(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, e, lane := fixtureLane(t)
	if err := os.WriteFile(filepath.Join(lane, "b.txt"), []byte("local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	review, err := e.Merge(ctx, "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Create(ctx, "other", CreateOptions{All: true}, false); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(e.WorkRoot, "sentinel.txt")
	if err = os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(lane); err != nil {
		t.Fatal(err)
	}

	listed, err := e.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	phases := map[string]WorkspaceView{}
	for _, item := range listed.Workspaces {
		phases[item.Name] = item
	}
	if phases["task"].Phase != "missing" || phases["other"].Phase != "ready" {
		t.Fatalf("list must flag only the deleted lane as missing: %+v", listed.Workspaces)
	}
	// WC-009: both the missing and the healthy lane report their branch.
	if phases["task"].Branch != "refs/heads/main" || phases["other"].Branch != "refs/heads/main" {
		t.Fatalf("list lost the branch: %+v", listed.Workspaces)
	}

	status, err := e.Status(ctx, "task")
	if code := failureCode(t, err); code != "workspace-missing" {
		t.Fatalf("status code %s", code)
	}
	if status.Workspace == nil || status.Workspace.Phase != "missing" {
		t.Fatalf("status must describe the missing lane: %+v", status.Workspace)
	}
	var failure *model.Failure
	errors.As(err, &failure)
	if !strings.Contains(failure.Message, filepath.Join(".workcopy-registry", "task.json")) || !strings.Contains(failure.Message, lane) {
		t.Fatalf("message must name the directory and the registry file: %s", failure.Message)
	}
	for _, dryRun := range []bool{true, false} {
		if _, err = e.Recover(ctx, "task", dryRun); failureCode(t, err) != "workspace-missing" {
			t.Fatalf("recover(dryRun=%v) must agree with status: %v", dryRun, err)
		}
	}

	if pins := strings.Fields(r.Git("for-each-ref", "--format=%(refname)", "refs/workcopy/")); len(pins) != 2 {
		t.Fatalf("both lanes should pin before clearing, got %q", pins)
	}
	registration := filepath.Join(e.WorkRoot, ".workcopy-registry", "task.json")
	planned, err := e.Destroy(ctx, "task", false, true)
	if err != nil || planned.Plan == nil {
		t.Fatalf("dry-run destroy: %+v %v", planned, err)
	}
	if _, err = os.Stat(registration); err != nil {
		t.Fatal("dry-run destroy removed the registration")
	}

	cleared, err := e.Destroy(ctx, "task", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(registration); !os.IsNotExist(err) {
		t.Fatal("registration survived destroy")
	}
	// Only the surviving lane may still pin its baseline.
	if pins := strings.Fields(r.Git("for-each-ref", "--format=%(refname)", "refs/workcopy/")); len(pins) != 1 || pins[0] == "" {
		t.Fatalf("expected exactly the other lane's pin, got %q", pins)
	}
	if _, err = os.Stat(review.Review.Path); err != nil {
		t.Fatal("destroy removed the review worktree")
	}
	joined := strings.Join(cleared.NextCommands, "\n")
	if !strings.Contains(joined, "worktree remove") || !strings.Contains(joined, review.Review.Path) {
		t.Fatalf("destroy must print the exact worktree removal command: %q", joined)
	}
	if body, readErr := os.ReadFile(sentinel); readErr != nil || string(body) != "keep" {
		t.Fatal("destroy touched files outside the lane")
	}
	after, err := e.List(ctx)
	if err != nil || len(after.Workspaces) != 1 || after.Workspaces[0].Name != "other" {
		t.Fatalf("list after clearing: %+v %v", after.Workspaces, err)
	}
	if _, err = e.Status(ctx, "other"); err != nil {
		t.Fatalf("the neighbouring lane must stay healthy: %v", err)
	}
}

func TestMissingLaneDoesNotMaskOtherRegistrationErrors(t *testing.T) {
	t.Parallel()
	r := testutil.NewRepository(t)
	r.Write("a", "base")
	r.Commit("base")
	e, err := New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Destroy(context.Background(), "never-registered", true, false); failureCode(t, err) != "workspace-not-registered" {
		t.Fatalf("unregistered names keep their error: %v", err)
	}
}

// WC-004: a review that never reached "prepared" and cannot be resumed must be
// removable without hand-editing metadata, and must never touch files.
func TestAbandonReviewDropsOnlyUnfinishedReceiptsAndKeepsFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, e, lane := fixtureLane(t)
	if err := os.WriteFile(filepath.Join(lane, "b.txt"), []byte("local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.Merge(ctx, "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if abandoned, abandonErr := e.RecoverWith(ctx, "task", RecoverOptions{AbandonReview: true}, false); abandonErr != nil || abandoned.ExitCode != 4 {
		t.Fatalf("a prepared review is not abandonable: %+v %v", abandoned, abandonErr)
	}

	// Simulate an interrupted preparation that someone then edited by hand.
	opened, err := e.openLane("task")
	if err != nil {
		t.Fatal(err)
	}
	receipt := *prepared.Review
	receipt.State = "preparing"
	if _, err = opened.store.PutBlob([]byte("local\n")); err != nil {
		t.Fatal(err)
	}
	if _, err = replaceReview(opened.store, receipt); err != nil {
		t.Fatal(err)
	}
	opened.close()
	if err = os.WriteFile(filepath.Join(receipt.Path, "b.txt"), []byte("hand edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Recover(ctx, "task", false); failureCode(t, err) != "review-changed" {
		t.Fatalf("ordinary recovery must refuse to overwrite the edit: %v", err)
	}
	if _, err = e.Status(ctx, "task"); failureCode(t, err) != "recovery-required" {
		t.Fatalf("the lane stays blocked until the owner decides: %v", err)
	}

	planned, err := e.RecoverWith(ctx, "task", RecoverOptions{AbandonReview: true}, true)
	if err != nil || planned.Plan == nil || planned.Plan.Action != "abandon-review" {
		t.Fatalf("dry run: %+v %v", planned, err)
	}
	if _, err = e.Status(ctx, "task"); failureCode(t, err) != "recovery-required" {
		t.Fatal("dry-run abandon changed state")
	}
	done, err := e.RecoverWith(ctx, "task", RecoverOptions{AbandonReview: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if body, readErr := os.ReadFile(filepath.Join(receipt.Path, "b.txt")); readErr != nil || string(body) != "hand edited\n" {
		t.Fatal("abandoning deleted or altered review files")
	}
	if !strings.Contains(strings.Join(done.NextCommands, "\n"), receipt.Path) {
		t.Fatalf("the retained review must be named with its removal command: %+v", done.NextCommands)
	}
	if _, err = e.Status(ctx, "task"); err != nil {
		t.Fatalf("lane must be usable again: %v", err)
	}
	fresh, err := e.Merge(ctx, "task", MergeOptions{}, false)
	if err != nil || fresh.Review == nil || fresh.Review.ID == receipt.ID {
		t.Fatalf("a new review must be preparable: %+v %v", fresh.Review, err)
	}
}
