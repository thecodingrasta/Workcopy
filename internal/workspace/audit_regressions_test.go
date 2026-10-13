package workspace

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"workcopy/internal/fsops"
	"workcopy/internal/model"
	"workcopy/internal/state"
	"workcopy/internal/testutil"
)

func TestMainIndexCannotExposeIgnoredLaneFiles(t *testing.T) {
	r, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, ".env"), []byte("private fixture"), 0600)
	r.Write(".env", "different source fixture")
	r.Git("add", "-f", ".env")
	report, err := e.Status(context.Background(), "task")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range report.Changes {
		if c.Path == ".env" {
			t.Fatal("source index exposed ignored lane file")
		}
	}
}
func TestTenOfTwentyFiveProfileAndRepeatedExpansion(t *testing.T) {
	r := testutil.NewFixtureRepository(t, "large-monorepo")
	e, err := New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal("open fixture repository:", err)
	}
	created, err := e.Create(context.Background(), "task", CreateOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(created.Workspace.Path, "src"))
	if err != nil || len(entries) != 10 {
		t.Fatal("daily profile did not materialize exactly ten project directories:", len(entries), err)
	}
	for index, entry := range entries {
		if expected := fmt.Sprintf("project-%02d", index+1); entry.Name() != expected {
			t.Fatalf("daily profile selected %s at position %d; expected %s", entry.Name(), index, expected)
		}
	}
	for _, required := range []string{"README.md", ".workcopy.json", "Directory.Build.props", "Monorepo.sln"} {
		if _, err = os.Stat(filepath.Join(created.Workspace.Path, required)); err != nil {
			t.Errorf("root file missing from lane: %s: %v", required, err)
		}
	}
	if _, err = os.Stat(filepath.Join(created.Workspace.Path, ".git")); !os.IsNotExist(err) {
		t.Fatal("lane contains a Git repository")
	}
	path := filepath.Join(created.Workspace.Path, "src", "project-01", "Component01.cs")
	os.WriteFile(path, []byte("local edit\n"), 0644)
	for _, id := range []string{"project-11", "project-12"} {
		if _, err = e.Expand(context.Background(), "task", []string{id}, false); err != nil {
			t.Fatal(err)
		}
	}
	if report, expandErr := e.Expand(context.Background(), "task", []string{"project-12"}, false); expandErr != nil || report.ExitCode != 4 {
		t.Fatal(report, expandErr)
	}
	content, _ := os.ReadFile(path)
	if string(content) != "local edit\n" {
		t.Fatal("expansion overwrote edit")
	}
	if _, err = e.Expand(context.Background(), "task", []string{"does-not-exist"}, false); err == nil {
		t.Fatal("unknown project accepted")
	}
	filter, err := e.Solution(context.Background(), "task", "Monorepo.sln", false)
	if err != nil {
		t.Fatal(err)
	}
	filterBytes, err := os.ReadFile(filepath.Join(created.Workspace.Path, filepath.FromSlash(filter.Plan.Files[0])))
	if err != nil {
		t.Fatal(err)
	}
	var solutionFilter struct {
		Solution struct {
			Projects []string `json:"projects"`
		} `json:"solution"`
	}
	if err = json.Unmarshal(filterBytes, &solutionFilter); err != nil || len(solutionFilter.Solution.Projects) != 12 {
		t.Fatalf("solution filter did not track the selected and expanded projects: %+v (%v)", solutionFilter, err)
	}
	foundFirst, foundExpanded := false, false
	for _, project := range solutionFilter.Solution.Projects {
		foundFirst = foundFirst || project == `src\project-01\project-01.csproj`
		foundExpanded = foundExpanded || project == `src\project-12\project-12.csproj`
	}
	if !foundFirst || !foundExpanded {
		t.Fatalf("solution filter did not preserve project paths from the solution: %+v", solutionFilter.Solution.Projects)
	}
}
func TestSharingRejectsAncestorContainingLanes(t *testing.T) {
	e, _, cache := sharingFixture(t)
	if _, err := e.Share(context.Background(), "daily", "packages", filepath.Dir(cache), "fixture", true, false); err == nil {
		t.Fatal("sharing introduced recursive access to lanes")
	}
}
func TestUntrackedFilesInReviewBlockApproval(t *testing.T) {
	_, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "b.txt"), []byte("local\n"), 0644)
	report, err := e.Merge(context.Background(), "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(report.Review.Path, ".env"), []byte("review-local fixture"), 0600)
	if _, err = e.Merge(context.Background(), "task", MergeOptions{Commit: true, Approve: true, ReviewID: report.Review.ID, Message: "review"}, false); err == nil {
		t.Fatal("ignored local review file was not checked")
	}
}
func TestAttachedReviewHEADCannotModifyMain(t *testing.T) {
	r, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "b.txt"), []byte("local\n"), 0644)
	report, err := e.Merge(context.Background(), "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	head := r.Git("rev-parse", "HEAD")
	gitAt(t, report.Review.Path, "symbolic-ref", "HEAD", "refs/heads/main")
	if _, err = e.Merge(context.Background(), "task", MergeOptions{Commit: true, Approve: true, ReviewID: report.Review.ID, Message: "review"}, false); err == nil {
		t.Fatal("attached head accepted")
	}
	if r.Git("rev-parse", "HEAD") != head {
		t.Fatal("main advanced")
	}
}
func TestSourceSymlinksArePreservedButEscapingLinksAreRejected(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("target", "base")
	if err := os.Symlink("target", filepath.Join(r.Path, "link")); err != nil {
		t.Skip(err)
	}
	r.Commit("link")
	e, _ := New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
	report, err := e.Create(context.Background(), "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(report.Workspace.Path, "link"))
	if err != nil || target != "target" {
		t.Fatal(target, err)
	}
	os.Symlink("../outside", filepath.Join(r.Path, "escape"))
	r.Commit("escaping link")
	if _, err = e.Create(context.Background(), "bad", CreateOptions{All: true}, false); err == nil {
		t.Fatal("escaping source link accepted")
	}
}
func TestRecoveryCompletesPartialSourceUpdate(t *testing.T) {
	_, e, lane := fixtureLane(t)
	opened, err := e.openLane("task")
	if err != nil {
		t.Fatal(err)
	}
	next := newGeneration(opened.snapshot)
	tx, err := opened.store.Begin(next)
	if err != nil {
		t.Fatal(err)
	}
	after := model.Version(model.Regular, 0644, []byte("recovered\n"))
	if err = tx.Add("b.txt", next.Baseline.Files["b.txt"], after, []byte("recovered\n"), false); err != nil {
		t.Fatal(err)
	}
	if err = tx.Prepare(); err != nil {
		t.Fatal(err)
	}
	opened.close()
	if _, err = e.Status(context.Background(), "task"); err == nil {
		t.Fatal("status ignored unfinished journal")
	}
	if _, err = e.Recover(context.Background(), "task", false); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(lane, "b.txt"))
	if string(body) != "recovered\n" {
		t.Fatal(string(body))
	}
}
func TestConflictContentUsesAcceptedBaselineAfterEarlierSync(t *testing.T) {
	r, e, lane := fixtureLane(t)
	r.Write("b.txt", "accepted upstream\n")
	r.Commit("upstream one")
	if _, err := e.Sync(context.Background(), "task", false); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(lane, "b.txt"), []byte("local two\n"), 0644)
	r.Write("b.txt", "upstream two\n")
	r.Commit("upstream two")
	synced, err := e.Sync(context.Background(), "task", false)
	if err != nil || len(synced.Conflicts) != 1 {
		t.Fatal(synced, err)
	}
	content, err := e.Content(context.Background(), "task", synced.Conflicts[0].ID, "base", 0, 65536)
	if err != nil || content.Content.SHA256 != model.Hash([]byte("accepted upstream\n")) {
		t.Fatal(content, err)
	}
}

func TestContentDoesNotReadConflictObjectsDuringWriterMutation(t *testing.T) {
	r, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "b.txt"), []byte("local edit\n"), 0644)
	r.Write("b.txt", "upstream edit\n")
	r.Commit("conflicting upstream edit")
	synced, err := e.Sync(context.Background(), "task", false)
	if err != nil || len(synced.Conflicts) != 1 {
		t.Fatalf("sync conflict = %+v, error %v", synced.Conflicts, err)
	}

	child := exec.Command(os.Args[0], "-test.run=^TestContentWriterLockHelper$", "--", lane)
	child.Env = append(os.Environ(), "WORKCOPY_CONTENT_LOCK_HELPER=1")
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || ready != "ready\n" {
		t.Fatalf("writer helper did not acquire lane lock: %q: %v", ready, err)
	}

	_, err = e.Content(context.Background(), "task", synced.Conflicts[0].ID, "local", 0, 65536)
	var failure *model.Failure
	if !errors.As(err, &failure) || failure.Exit != 3 || failure.Code != "workspace-busy" {
		t.Fatalf("content during writer mutation returned error %v, want workspace-busy", err)
	}
	if _, err = stdin.Write([]byte("release\n")); err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(); err != nil {
		t.Fatalf("writer helper: %v", err)
	}
}

func TestContentWriterLockHelper(t *testing.T) {
	if os.Getenv("WORKCOPY_CONTENT_LOCK_HELPER") != "1" {
		return
	}
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		panic(fmt.Sprintf("missing lane path: %v", os.Args))
	}
	root, err := fsops.OpenRoot(os.Args[len(os.Args)-1])
	if err != nil {
		panic(err)
	}
	defer root.Close()
	lock, err := state.Acquire(root, ".workcopy/lock")
	if err != nil {
		panic(err)
	}
	defer lock.Close()
	fmt.Fprintln(os.Stdout, "ready")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func TestConflictsSurviveRestartAndNewUpstreamCommits(t *testing.T) {
	r, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "b.txt"), []byte("local\n"), 0644)
	r.Write("b.txt", "upstream\n")
	r.Commit("conflict")
	first, err := e.Sync(context.Background(), "task", false)
	if err != nil {
		t.Fatal(err)
	}
	r.Write("a.txt", "later unrelated upstream\n")
	r.Commit("later")
	fresh, _ := New(context.Background(), r.Path, e.WorkRoot)
	second, err := fresh.Sync(context.Background(), "task", false)
	if err != nil || len(second.Conflicts) != 1 || second.Conflicts[0].ID != first.Conflicts[0].ID {
		t.Fatal(second, err)
	}
}
func TestCopiedLaneCannotBeDestroyedAsOriginal(t *testing.T) {
	_, e, lane := fixtureLane(t)
	body, _ := os.ReadFile(filepath.Join(lane, ".workcopy", "manifest.json"))
	var snapshot model.Snapshot
	json.Unmarshal(body, &snapshot)
	snapshot.Manifest.ID = strings.Repeat("f", 32)
	changed, _ := json.Marshal(snapshot)
	os.WriteFile(filepath.Join(lane, ".workcopy", "manifest.json"), changed, 0600)
	if _, err := e.Destroy(context.Background(), "task", true, false); err == nil {
		t.Fatal("ownership mismatch accepted")
	}
}
func TestLocalResolutionChangedAfterTokenIsRejected(t *testing.T) {
	r, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "b.txt"), []byte("local\n"), 0644)
	r.Write("b.txt", "upstream\n")
	r.Commit("conflict")
	report, _ := e.Sync(context.Background(), "task", false)
	token := report.Conflicts[0].ID
	os.WriteFile(filepath.Join(lane, "b.txt"), []byte("newer local edit\n"), 0644)
	if _, err := e.Resolve(context.Background(), "task", "b.txt", ResolveOptions{UseUpstream: true, ExpectedConflict: token}, false); err == nil {
		t.Fatal("stale local content overwritten")
	}
	if _, err := e.Resolve(context.Background(), "task", "b.txt", ResolveOptions{UseLocal: true, ExpectedConflict: token, ExpectedLocal: model.Hash([]byte("newer local edit\n"))}, false); err != nil {
		t.Fatal(err)
	}
}
func TestNoUnexpectedTransactionJunkAfterSuccessfulCreate(t *testing.T) {
	_, _, lane := fixtureLane(t)
	if _, err := os.Stat(filepath.Join(lane, filepath.FromSlash(state.JournalPath))); !os.IsNotExist(err) {
		t.Fatal("journal retained")
	}
	entries, err := os.ReadDir(filepath.Join(lane, ".workcopy", "objects"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("source blobs duplicated after creation")
	}
}

func TestExplicitConfigRequiresSchemaVersion(t *testing.T) {
	r, e, _ := fixtureLane(t)
	r.Write("empty.json", `{}`)
	_, err := e.Create(context.Background(), "second", CreateOptions{ConfigPath: filepath.Join(r.Path, "empty.json")}, true)
	if err == nil {
		t.Fatal("missing schema accepted")
	}
}

func TestLocalCaseCollisionIsRejectedBeforeReviewCreation(t *testing.T) {
	_, e, lane := fixtureLane(t)
	if _, err := os.Stat(filepath.Join(lane, "A.txt")); err == nil {
		t.Skip("case-insensitive filesystem cannot represent the conflicting fixture")
	}
	if err := os.WriteFile(filepath.Join(lane, "A.txt"), []byte("different file"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Merge(context.Background(), "task", MergeOptions{}, false); err == nil {
		t.Fatal("nonportable local collision reached review preparation")
	}
	opened, err := e.openLane("task")
	if err != nil {
		t.Fatal("rejection left a pending operation:", err)
	}
	defer opened.close()
	if len(opened.snapshot.Manifest.Reviews) != 0 {
		t.Fatal("collision created a review receipt")
	}
}
