package workspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"workcopy/internal/testutil"
)

// unreliableModeRoot returns a directory on a filesystem that reports every
// file as executable when the environment provides one (a vfat loop mount in
// CI, a /mnt/c drive in WSL). Otherwise the caller simulates the symptom.
func unreliableModeRoot(t *testing.T) (string, bool) {
	t.Helper()
	base := os.Getenv("WORKCOPY_UNRELIABLE_MODE_FS")
	if base == "" {
		return "", false
	}
	directory, err := os.MkdirTemp(base, "wc-mode-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	// Refuse to run a misconfigured environment: the test is only meaningful if
	// a file created without execute bits really is reported as executable.
	probe := filepath.Join(directory, "premise")
	if err = os.WriteFile(probe, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if info, statErr := os.Stat(probe); statErr != nil || info.Mode().Perm()&0111 == 0 {
		t.Fatalf("WORKCOPY_UNRELIABLE_MODE_FS=%s does not report new files as executable (mode %v); mount a drvfs without metadata or a FAT volume with fmask=0000", base, info.Mode().Perm())
	}
	return directory, true
}

func makeEverythingExecutable(t *testing.T, directory string) {
	t.Helper()
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.Type()&os.ModeSymlink != 0 {
			return walkErr
		}
		return os.Chmod(path, 0777)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// WC-001 and WC-004: on a filesystem that reports every file as executable a
// fresh lane must show no changes, edits must show as exactly the edits, and
// review, approval and cleanup must all still work.
func TestLaneOnFilesystemThatReportsEveryFileExecutable(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows never tracks execute bits; covered by the Unix runs")
	}
	ctx := context.Background()
	r := testutil.NewRepository(t)
	r.Write("a.txt", "one\ntwo\n")
	r.Write("run.sh", "#!/bin/sh\necho hi\n")
	r.Commit("base")
	// Set the committed mode in the index: chmod does not stick on every
	// filesystem the fixture may live on, and Git for Windows ignores it.
	r.Git("update-index", "--chmod=+x", "run.sh")
	r.Git("commit", "--amend", "--no-edit")
	if !strings.HasPrefix(r.Git("ls-tree", "HEAD", "run.sh"), "100755") {
		t.Fatal("fixture precondition: run.sh must be committed executable")
	}
	root := filepath.Join(r.Parent, "lanes")
	real := false
	if mount, ok := unreliableModeRoot(t); ok {
		root, real = filepath.Join(mount, "lanes"), true
	}
	e, err := New(ctx, r.Path, root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	if real {
		manifest, inspectErr := e.Inspect(ctx, "task")
		if inspectErr != nil || manifest.RespectExecutableBits {
			t.Fatalf("create must probe the filesystem and stop tracking execute bits: %v %+v", inspectErr, manifest.RespectExecutableBits)
		}
	} else {
		makeEverythingExecutable(t, lane)
	}

	status, err := e.Status(ctx, "task")
	if err != nil || len(status.Changes) != 0 {
		t.Fatalf("a fresh lane must be clean on this filesystem: %+v %v", status.Changes, err)
	}
	if err = os.WriteFile(filepath.Join(lane, "a.txt"), []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	status, err = e.Status(ctx, "task")
	if err != nil || len(status.Changes) != 1 || status.Changes[0].Path != "a.txt" {
		t.Fatalf("only the real edit may be reported: %+v %v", status.Changes, err)
	}

	review, err := e.Merge(ctx, "task", MergeOptions{}, false)
	if err != nil {
		t.Fatalf("review preparation must succeed: %v", err)
	}
	if again, reuseErr := e.Merge(ctx, "task", MergeOptions{}, false); reuseErr != nil || again.Review == nil || again.Review.ID != review.Review.ID {
		t.Fatalf("an unchanged review must be reused: %+v %v", again.Review, reuseErr)
	}
	if recovered, recoverErr := e.Recover(ctx, "task", false); recoverErr != nil || recovered.ExitCode != 4 {
		t.Fatalf("a healthy lane has nothing to recover: %+v %v", recovered, recoverErr)
	}
	committed, err := e.Merge(ctx, "task", MergeOptions{Commit: true, Approve: true, ReviewID: review.Review.ID, Message: "edit a"}, false)
	if err != nil {
		t.Fatal(err)
	}
	tree := r.Git("ls-tree", committed.Review.Commit)
	if !strings.Contains(tree, "100755 blob") || !strings.Contains(tree, "\trun.sh") {
		t.Fatalf("the executable script lost its mode in the review commit:\n%s", tree)
	}
	if strings.Contains(r.Git("ls-tree", committed.Review.Commit, "a.txt"), "100755") {
		t.Fatal("a plain file gained an execute bit in the review commit")
	}

	if err = os.WriteFile(filepath.Join(lane, "a.txt"), []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Destroy(ctx, "task", false, false); err != nil {
		t.Fatalf("cleanup of a clean lane must not require --force here: %v", err)
	}
}

func TestRepositoryFileModeSettingDisablesExecuteBitTracking(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows never tracks execute bits")
	}
	probe := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(probe, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(probe); err != nil || info.Mode().Perm()&0111 != 0 {
		t.Skip("the temporary filesystem does not store permission bits, so there is no mode tracking to control")
	}
	ctx := context.Background()
	for _, tracked := range []bool{true, false} {
		r := testutil.NewRepository(t)
		r.Write("tool.sh", "#!/bin/sh\n")
		r.Commit("base")
		if !tracked {
			r.Git("config", "core.fileMode", "false")
		}
		e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
		if err != nil {
			t.Fatal(err)
		}
		created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(filepath.Join(created.Workspace.Path, "tool.sh"), 0755); err != nil {
			t.Fatal(err)
		}
		status, err := e.Status(ctx, "task")
		if err != nil {
			t.Fatal(err)
		}
		changed := len(status.Changes) == 1
		if tracked && !changed {
			t.Fatal("control: chmod +x must be a change when mode tracking is on")
		}
		if !tracked && len(status.Changes) != 0 {
			t.Fatalf("core.fileMode=false must be honoured: %+v", status.Changes)
		}
	}
}
