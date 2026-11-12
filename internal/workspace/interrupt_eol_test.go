package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"workcopy/internal/testutil"
)

// convertedRepository is the OLP situation: every text file is converted to CRLF in
// the lane and stored as LF in Git, so the bytes on disk never equal the blob.
func convertedRepository(t *testing.T, files int) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write(".gitattributes", "* text eol=crlf\n*.bin -text\n")
	for n := 0; n < files; n++ {
		r.Write(fmt.Sprintf("src/p%02d/dir%d/file%04d.txt", n%7, n%3, n), fmt.Sprintf("content %d\nsecond line\n", n))
	}
	r.Write("assets/blob.bin", "binary\r\nstays as written\r\n")
	r.Write("empty.txt", "")
	r.Commit("crlf repository")
	return r
}

// WC-018: an interrupted create on a repository whose files need line-ending
// conversion could not be recovered. The replay compared the CRLF bytes already
// on disk with the LF blob and called a correctly written file "changed".
func TestInterruptedCreationRecoversWhenFilesNeedLineEndingConversion(t *testing.T) {
	t.Parallel()
	r := convertedRepository(t, 600)
	background := context.Background()
	e, err := New(background, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, cutAt := range []int{1, 90, 300} {
		name := fmt.Sprintf("cut%d", cutAt)
		ctx, cancel := cancelAfter(cutAt)
		if _, err = e.Create(ctx, name, CreateOptions{All: true}, false); err == nil {
			cancel()
			t.Fatalf("%s: the interrupted creation must report failure", name)
		}
		cancel()
		recovered, recoverErr := e.Recover(background, name, false)
		if recoverErr != nil {
			t.Fatalf("%s: recover must finish the creation, got: %v", name, recoverErr)
		}
		if !strings.Contains(recovered.Message, "recovered") {
			t.Errorf("%s: unexpected recover message %q", name, recovered.Message)
		}
		status, statusErr := e.Status(background, name)
		if statusErr != nil || len(status.Changes) != 0 {
			t.Fatalf("%s: a recovered lane must be clean: %+v %v", name, status.Changes, statusErr)
		}
		lane := filepath.Join(e.WorkRoot, name)
		body, readErr := os.ReadFile(filepath.Join(lane, "src", "p00", "dir0", "file0000.txt"))
		if readErr != nil || string(body) != "content 0\r\nsecond line\r\n" {
			t.Fatalf("%s: a converted file must be CRLF on disk: %q %v", name, body, readErr)
		}
		raw, readErr := os.ReadFile(filepath.Join(lane, "assets", "blob.bin"))
		if readErr != nil || string(raw) != "binary\r\nstays as written\r\n" {
			t.Fatalf("%s: a -text file must be untouched: %q %v", name, raw, readErr)
		}
	}
}

// WC-019: killing a create leaves .workcopy-tmp-* files from interrupted atomic
// writes. After recovery they were reported by status as the lane's own work.
var tempArtifact = regexp.MustCompile(`^\.workcopy-tmp-[0-9a-f]{16,}$`)

func TestRecoveredCreationLeavesNoTemporaryFilesInStatus(t *testing.T) {
	t.Parallel()
	r := wideRepository(t, 400)
	background := context.Background()
	e, err := New(background, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := cancelAfter(150)
	defer cancel()
	if _, err = e.Create(ctx, "task", CreateOptions{All: true}, false); err == nil {
		t.Fatal("the interrupted creation must report failure")
	}
	// A process killed inside an atomic write leaves its temporary file in the\n\t// directory it was writing to, so the strays go where this journal writes.
	lane := filepath.Join(e.WorkRoot, "task")
	stray := []string{
		filepath.Join(lane, "src", "p00", "dir0", ".workcopy-tmp-0123456789abcdef0123456789abcdef"),
		filepath.Join(lane, "src", "p01", "dir1", ".workcopy-tmp-fedcba9876543210fedcba9876543210"),
	}
	for _, name := range stray {
		if err = os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(name, []byte("partial"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = e.Recover(background, "task", false); err != nil {
		t.Fatal(err)
	}
	status, err := e.Status(background, "task")
	if err != nil || len(status.Changes) != 0 {
		t.Fatalf("a recovered lane must be clean, status lists: %+v %v", status.Changes, err)
	}
	for _, name := range stray {
		if _, statErr := os.Lstat(name); !os.IsNotExist(statErr) {
			t.Errorf("recovery must remove its own temporary file %s: %v", name, statErr)
		}
	}
	// A person's file that merely looks similar is never touched.
	keep := filepath.Join(lane, "src", "p00", "dir0", ".workcopy-tmp-notes.txt")
	if err = os.WriteFile(keep, []byte("mine"), 0644); err != nil {
		t.Fatal(err)
	}
	if status, err = e.Status(background, "task"); err != nil || len(status.Changes) != 1 || status.Changes[0].Path != "src/p00/dir0/.workcopy-tmp-notes.txt" {
		t.Fatalf("an ordinary file with a similar name is the user's work: %+v %v", status.Changes, err)
	}
	_ = tempArtifact
}

// WC-020: an MCP create cut off by its deadline reported "statat
// .workcopy/objects/...: The system cannot find the path" instead of the
// cancellation. The cut-off Git read fell through to the blob store, whose miss
// replaced the real cause. Whatever the interleaving, a context that ended must
// be what the caller sees, and the lane must stay recoverable.
func TestCancelledCreateReportsTheCancellationNotAnOSError(t *testing.T) {
	t.Parallel()
	r := convertedRepository(t, 500)
	background := context.Background()
	e, err := New(background, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, cutAt := range []int{1, 64, 256} {
		name := fmt.Sprintf("cancel%d", cutAt)
		ctx, cancel := cancelAfter(cutAt)
		_, err = e.Create(ctx, name, CreateOptions{All: true}, false)
		cancel()
		if err == nil {
			t.Fatalf("%s: the cut-off creation must fail", name)
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s: the error must be the cancellation, got: %v", name, err)
		}
		if strings.Contains(err.Error(), "statat") || strings.Contains(err.Error(), "cannot find the path") {
			t.Errorf("%s: a raw OS error leaked into the message: %v", name, err)
		}
		if _, recoverErr := e.Recover(background, name, false); recoverErr != nil {
			t.Fatalf("%s: a cut-off creation must recover: %v", name, recoverErr)
		}
	}
	// A deadline is reported the same way as an explicit cancel.
	ctx, stop := context.WithTimeout(background, time.Nanosecond)
	defer stop()
	<-ctx.Done()
	if _, err = e.Create(ctx, "deadline", CreateOptions{All: true}, false); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("an expired deadline must surface as DeadlineExceeded: %v", err)
	}
}
