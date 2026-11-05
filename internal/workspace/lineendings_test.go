package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"workcopy/internal/testutil"
)

func numbered(prefix string, count int) string {
	var body strings.Builder
	for n := 1; n <= count; n++ {
		fmt.Fprintf(&body, "%s line %d\n", prefix, n)
	}
	return body.String()
}

func toCRLF(text string) string { return strings.ReplaceAll(text, "\n", "\r\n") }

// lineEndingRepository commits blobs exactly as written (no attributes yet, so
// Git cannot normalise them) and only then adds the attributes. This is how a
// repository that adopted .gitattributes later looks, and it lets the fixture
// hold blobs that already contain carriage returns.
//
// legacy adds blobs that already contain carriage returns. Git itself reports
// such files as modified under eol=crlf (they need renormalising), so tests that
// read git status in a review leave them out.
func lineEndingRepository(t *testing.T, legacy bool) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write("app/Big.cs", numbered("cs", 40))
	r.Write("app/Other.cs", "class Other {}\n")
	r.Write("notes/Big.txt", numbered("txt", 40))
	r.Write("raw.dat", "x\r\ny\n")
	if legacy {
		r.Write("pure.cs", "a\r\nb\r\n")
		r.Write("mixed.cs", "a\r\nb\n")
	}
	r.Commit("content")
	r.Write(".gitattributes", "*.cs text eol=crlf\n*.txt text eol=lf\n*.dat -text\n")
	// Add only the attributes file: a blanket add could re-normalise the blobs
	// above, which is exactly what this fixture must not let Git do.
	r.Git("add", ".gitattributes")
	r.Git("commit", "-m", "attributes")
	return r
}

func readLane(t *testing.T, lane, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(lane, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func writeLane(t *testing.T, lane, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(lane, filepath.FromSlash(name)), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

// WC-005 (a): a lane is written the way the repository's own checkout is.
func TestLaneMirrorsTheRepositoriesCheckout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := lineEndingRepository(t, true)
	clone := filepath.Join(r.Parent, "clone")
	r.GitAt(r.Parent, "", "clone", "-q", r.Path, clone)
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	for _, name := range []string{"app/Big.cs", "app/Other.cs", "notes/Big.txt", "raw.dat", "pure.cs", ".gitattributes"} {
		if got, want := readLane(t, lane, name), readLane(t, clone, name); !bytes.Equal(got, want) {
			t.Errorf("%s differs from git's own checkout:\nlane  %q\nclone %q", name, got, want)
		}
	}
	if !bytes.Contains(readLane(t, lane, "app/Big.cs"), []byte("\r\n")) || bytes.Contains(readLane(t, lane, "notes/Big.txt"), []byte("\r")) {
		t.Fatal("eol=crlf must produce CRLF and eol=lf must produce LF")
	}
	// Content that already mixes endings is left exactly as committed: it could
	// not be converted back, and a fresh lane must never be dirty.
	if got := readLane(t, lane, "mixed.cs"); string(got) != "a\r\nb\n" {
		t.Errorf("mixed endings must be preserved, got %q", got)
	}
	status, err := e.Status(ctx, "task")
	if err != nil || len(status.Changes) != 0 {
		t.Fatalf("a fresh lane must be clean: %+v %v", status.Changes, err)
	}
}

// WC-005 (b): an editor that rewrites line endings is not a change, and it can
// no longer turn an unrelated upstream edit into a conflict.
func TestLineEndingRewritesNeitherChangeNorConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := lineEndingRepository(t, true)
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path

	// Opposite of each file's own form, as Visual Studio or a Unix tool would.
	writeLane(t, lane, "app/Other.cs", "class Other {}\n") // rewritten, content identical
	writeLane(t, lane, "notes/Big.txt", toCRLF(strings.Replace(numbered("txt", 40), "txt line 5\n", "txt line 5 edited\n", 1)))
	writeLane(t, lane, "app/Big.cs", strings.Replace(numbered("cs", 40), "cs line 6\n", "cs line 6 edited\n", 1))
	status, err := e.Status(ctx, "task")
	if err != nil {
		t.Fatal(err)
	}
	var changed []string
	for _, change := range status.Changes {
		changed = append(changed, change.Path)
	}
	sort.Strings(changed)
	if strings.Join(changed, ",") != "app/Big.cs,notes/Big.txt" {
		t.Fatalf("only real edits may be reported, got %v", changed)
	}

	r.Write("notes/Big.txt", strings.Replace(numbered("txt", 40), "txt line 35\n", "txt line 35 upstream\n", 1))
	r.Write("app/Big.cs", strings.Replace(numbered("cs", 40), "cs line 35\n", "cs line 35 upstream\n", 1))
	r.Git("add", "app/Big.cs", "notes/Big.txt")
	r.Git("commit", "-m", "upstream edits far from the local ones")
	synced, err := e.Sync(ctx, "task", false)
	if err != nil || synced.ExitCode != 0 || len(synced.Conflicts) != 0 {
		t.Fatalf("a line-ending rewrite must not create a conflict: %+v %v", synced, err)
	}
	cs := string(readLane(t, lane, "app/Big.cs"))
	if !strings.Contains(cs, "cs line 6 edited") || !strings.Contains(cs, "cs line 35 upstream") {
		t.Fatalf("both edits must survive the merge:\n%s", cs)
	}
	if strings.Count(cs, "\n") != strings.Count(cs, "\r\n") {
		t.Fatal("after a merge the file must be written in the repository's CRLF form")
	}
	txt := string(readLane(t, lane, "notes/Big.txt"))
	if !strings.Contains(txt, "txt line 5 edited") || !strings.Contains(txt, "txt line 35 upstream") || strings.Contains(txt, "\r") {
		t.Fatalf("an eol=lf file must come back as LF with both edits:\n%q", txt)
	}
}

// WC-005: reviews and commits use the repository's form on disk and Git's
// normalised form in history, so the diff shows only the real change.
func TestReviewAndCommitCarryOnlyTheRealChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := lineEndingRepository(t, false)
	base := r.Git("rev-parse", "HEAD")
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	writeLane(t, lane, "app/Big.cs", toCRLF(strings.Replace(numbered("cs", 40), "cs line 5\n", "cs line 5 edited\n", 1)))
	writeLane(t, lane, "app/New.cs", "class New\r\n{\r\n}\r\n")
	writeLane(t, lane, "new.dat", "p\r\nq\r\n")

	review, err := e.Merge(ctx, "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := readLane(t, review.Review.Path, "app/Big.cs"); !bytes.Contains(got, []byte("\r\n")) {
		t.Fatal("the review worktree must use the repository's CRLF form")
	}
	porcelain := strings.Split(strings.TrimSpace(r.GitAt(review.Review.Path, "", "status", "--porcelain")), "\n")
	for n := range porcelain {
		porcelain[n] = strings.TrimSpace(porcelain[n])
	}
	sort.Strings(porcelain)
	if want := []string{"?? app/New.cs", "?? new.dat", "M app/Big.cs"}; strings.Join(porcelain, "|") != strings.Join(want, "|") {
		t.Fatalf("git must see exactly the real changes in the review, got %q", porcelain)
	}

	committed, err := e.Merge(ctx, "task", MergeOptions{Commit: true, Approve: true, ReviewID: review.Review.ID, Message: "edit"}, false)
	if err != nil {
		t.Fatal(err)
	}
	commit := committed.Review.Commit
	if blob := r.Git("show", commit+":app/Big.cs"); strings.Contains(blob, "\r") {
		t.Fatal("history must hold the normalised LF form of an eol=crlf file")
	}
	if blob := r.Git("show", commit+":app/New.cs"); strings.Contains(blob, "\r") {
		t.Fatal("a new text file written with CRLF must be committed as LF")
	}
	if blob := r.Git("show", commit+":new.dat"); !strings.Contains(blob, "\r") {
		t.Fatal("a -text file must be committed byte for byte")
	}
	stat := r.Git("diff", "--numstat", base, commit, "--", "app/Big.cs")
	if !strings.HasPrefix(stat, "1\t1\t") {
		t.Fatalf("the commit must change one line, not the whole file: %q", stat)
	}
}

// core.autocrlf with no attributes at all: the lane follows the same rule git
// would use to write the checkout, including leaving binary content alone.
func TestLaneFollowsCoreAutoCRLF(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, setting := range []struct{ autocrlf, text string }{{"true", "t\r\nu\r\n"}, {"input", "t\nu\n"}, {"false", "t\nu\n"}} {
		r := testutil.NewRepository(t)
		r.Write("t.txt", "t\nu\n")
		r.Write("b.bin", "t\x00\nu\n")
		r.Commit("base")
		r.Git("config", "core.autocrlf", setting.autocrlf)
		e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
		if err != nil {
			t.Fatal(err)
		}
		created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(readLane(t, created.Workspace.Path, "t.txt")); got != setting.text {
			t.Errorf("core.autocrlf=%s: text file is %q, want %q", setting.autocrlf, got, setting.text)
		}
		if got := string(readLane(t, created.Workspace.Path, "b.bin")); got != "t\x00\nu\n" {
			t.Errorf("core.autocrlf=%s: binary file must not be converted, got %q", setting.autocrlf, got)
		}
		if status, statusErr := e.Status(ctx, "task"); statusErr != nil || len(status.Changes) != 0 {
			t.Errorf("core.autocrlf=%s: fresh lane not clean: %+v %v", setting.autocrlf, status.Changes, statusErr)
		}
	}
}
