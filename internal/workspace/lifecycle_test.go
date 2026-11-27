package workspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"workcopy/internal/testutil"
)

func fixtureLane(t *testing.T) (*testutil.Repository, *Engine, string) {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write("a.txt", "first\nsecond\nthird\nfourth\nfifth\n")
	r.Write("b.txt", "base\n")
	r.Write(".gitignore", "cache/\n.env\n")
	r.Commit("base")
	e, err := New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(context.Background(), "task", CreateOptions{AllowTempRoot: true, All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	return r, e, created.Workspace.Path
}
func TestCreateUsesSelectedSnapshotNotDirtyCheckout(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("a", "base")
	r.Commit("base")
	r.Git("checkout", "-b", "feature")
	r.Write("a", "feature")
	r.Commit("feature")
	r.Git("checkout", "main")
	r.Write("a", "dirty")
	e, err := New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := e.Create(context.Background(), "task", CreateOptions{AllowTempRoot: true, Branch: "feature", All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(report.Workspace.Path, "a"))
	if string(b) != "feature" {
		t.Fatal("wrong snapshot", string(b))
	}
	if _, err = os.Stat(filepath.Join(report.Workspace.Path, ".git")); !os.IsNotExist(err) {
		t.Fatal("lane contains .git")
	}
}

func TestCreateRejectsChangedAdmittedCommitBeforeWriting(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("a.txt", "original\n")
	r.Commit("admitted snapshot")
	expected := r.Git("rev-parse", "HEAD")
	e, err := New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	options := CreateOptions{AllowTempRoot: true, All: true, ExpectedCommit: expected}
	if _, err = e.Create(context.Background(), "worker", options, true); err != nil {
		t.Fatal(err)
	}
	r.Write("a.txt", "changed upstream\n")
	r.Commit("advance during admission")
	if _, err = e.Create(context.Background(), "worker", options, false); err == nil {
		t.Fatal("created a different snapshot from the admitted commit")
	}
	if _, err = os.Stat(e.WorkRoot); !os.IsNotExist(err) {
		t.Fatal("stale admitted creation persisted a work root")
	}
	if pins := r.Git("for-each-ref", "--format=%(refname)", "refs/workcopy/"); pins != "" {
		t.Fatal("stale creation left pins", pins)
	}
}
func TestDryRunCreateDoesNotCreateWorkRoot(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("a", "base")
	r.Commit("base")
	root := filepath.Join(r.Parent, "lanes")
	e, _ := New(context.Background(), r.Path, root)
	if _, err := e.Create(context.Background(), "task", CreateOptions{AllowTempRoot: true, All: true}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("dry run created root")
	}
}
func TestDestroyProtectsIgnoredLocalOnlyFiles(t *testing.T) {
	_, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, ".env"), []byte("local-only fixture"), 0600)
	if _, err := e.Destroy(context.Background(), "task", false, false); err == nil {
		t.Fatal("discarded ignored file")
	}
	b, err := os.ReadFile(filepath.Join(lane, ".env"))
	if err != nil || string(b) != "local-only fixture" {
		t.Fatal("lost local file")
	}
}

func TestDestroyRenamesCleanLaneBeforeQuarantineDeletion(t *testing.T) {
	_, e, lane := fixtureLane(t)
	nestedWorkingDirectory := filepath.Join(lane, "nested", "working-directory")
	if err := os.MkdirAll(nestedWorkingDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(nestedWorkingDirectory); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(workingDirectory)

	if _, err := e.Destroy(context.Background(), "task", false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lane); !os.IsNotExist(err) {
		t.Fatalf("lane remains: %v", err)
	}
	if runtime.GOOS == "windows" {
		currentDirectory, err := os.Getwd()
		if err != nil {
			t.Fatalf("get working directory after destroy: %v", err)
		}
		if containsPath(lane, currentDirectory) {
			t.Fatalf("working directory remains inside destroyed lane: %s", currentDirectory)
		}
	}
}

func TestDestroyRejectsUnownedDirectoryEvenWithForce(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("a", "base")
	r.Commit("base")
	root := filepath.Join(r.Parent, "lanes")
	os.MkdirAll(filepath.Join(root, "unknown"), 0700)
	e, _ := New(context.Background(), r.Path, root)
	if _, err := e.Destroy(context.Background(), "unknown", true, false); err == nil {
		t.Fatal("deleted unowned directory")
	}
}
func TestStatusIncludesRootChangesAndIgnoresUntrackedSecrets(t *testing.T) {
	_, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "a.txt"), []byte("changed"), 0644)
	os.WriteFile(filepath.Join(lane, ".env"), []byte("secret fixture"), 0600)
	r, err := e.Status(context.Background(), "task")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 1 || r.Changes[0].Path != "a.txt" {
		t.Fatalf("wrong changes: %+v", r.Changes)
	}
}

func TestCreateRefusesTempDirectoryWithoutAllowTempRoot(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("a", "base")
	r.Commit("base")
	root := filepath.Join(r.Parent, "lanes")
	e, err := New(context.Background(), r.Path, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Create(context.Background(), "task", CreateOptions{All: true}, true); failureCode(t, err) != "temp-work-root-refused" {
		t.Fatalf("expected temp-work-root-refused on dry-run, got: %v", err)
	}
	if _, err = e.Create(context.Background(), "task", CreateOptions{All: true}, false); failureCode(t, err) != "temp-work-root-refused" {
		t.Fatalf("expected temp-work-root-refused on apply, got: %v", err)
	}
}

func TestCreateWithLocalGitIsStandaloneNotWorktree(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("a.txt", "base\n")
	r.Commit("base")
	root := filepath.Join(r.Parent, "lanes")
	e, err := New(context.Background(), r.Path, root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created, err := e.Create(ctx, "task", CreateOptions{AllowTempRoot: true, All: true, LocalGit: true}, false)
	if err != nil {
		t.Fatalf("create with LocalGit failed: %v", err)
	}
	lane := created.Workspace.Path
	gitInfo, err := os.Lstat(filepath.Join(lane, ".git"))
	if err != nil || !gitInfo.IsDir() {
		t.Fatalf("lane .git must be a standalone directory, not a worktree gitfile: %v %v", gitInfo, err)
	}
	if remotes := r.GitAt(lane, "", "remote"); remotes != "" {
		t.Fatalf("lane git must have no remotes: %q", remotes)
	}
	if status := r.GitAt(lane, "", "status", "--porcelain"); status != "" {
		t.Fatalf("lane git status should be clean initially, got: %q", status)
	}
	if err := os.WriteFile(filepath.Join(lane, "b.txt"), []byte("lane edit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	r.GitAt(lane, "", "add", "b.txt")
	r.GitAt(lane, "", "-c", "user.name=Dev", "-c", "user.email=dev@example.invalid", "commit", "-m", "local lane commit")
	st, err := e.Status(ctx, "task")
	if err != nil || len(st.Changes) != 1 || st.Changes[0].Path != "b.txt" {
		t.Fatalf("workcopy status must still see lane edit after local git commit: %+v %v", st.Changes, err)
	}
	if _, err := e.Destroy(ctx, "task", true, false); err != nil {
		t.Fatalf("destroy LocalGit lane failed: %v", err)
	}
	if _, err := os.Stat(lane); !os.IsNotExist(err) {
		t.Fatalf("destroyed LocalGit lane still exists: %v", err)
	}
}
