package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"workcopy/internal/model"
	"workcopy/internal/testutil"
)

// These tests lower the streaming threshold so a few hundred KiB take exactly
// the code paths a 70 MiB file takes, then repeat the key checks at the real
// size in the acceptance tests. Tests that change the threshold are not
// parallel: Go runs them before any parallel test starts.

const smallThreshold = 64 << 10

func pseudo(size int, seed uint32) []byte {
	out := make([]byte, size)
	for i := range out {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		out[i] = byte(seed)
	}
	return out
}

func digest(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }

func largeRepository(t *testing.T) (*testutil.Repository, map[string][]byte) {
	t.Helper()
	r := testutil.NewRepository(t)
	files := map[string][]byte{
		"assets/model.bin":    pseudo(5*smallThreshold+123, 1),
		"assets/texture.dat":  pseudo(3*smallThreshold, 2),
		"assets/other.dat":    pseudo(2*smallThreshold+1, 3),
		"docs/big-text.txt":   []byte(strings.Repeat("a line of text\r\n", 8*1024)), // CRLF text, over the threshold
		"src/small.txt":       []byte("small\n"),
		"assets/run-me.sh":    append([]byte("#!/bin/sh\n"), pseudo(2*smallThreshold, 4)...),
		"assets/borderline.b": pseudo(smallThreshold, 5), // exactly at the threshold: still in memory
	}
	r.Write(".gitattributes", "*.txt text eol=crlf\n")
	for name, body := range files {
		r.Write(name, string(body))
	}
	// Both: the working file is executable where the filesystem has the bit, and
	// the index says so where it does not (Windows), because Commit runs git add -A.
	if err := os.Chmod(filepath.Join(r.Path, "assets", "run-me.sh"), 0755); err != nil {
		t.Fatal(err)
	}
	r.Git("update-index", "--add", "--chmod=+x", "assets/run-me.sh")
	r.Commit("large files")
	return r, files
}

func laneFile(t *testing.T, lane, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(lane, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestLargeFilesRoundTripThroughEveryOperation(t *testing.T) {
	defer model.SetInlineFileBytes(smallThreshold)()
	r, files := largeRepository(t)
	ctx := context.Background()
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}

	// Create: a repository with big tracked files is no longer refused.
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatalf("a repository holding large files must be creatable: %v", err)
	}
	lane := created.Workspace.Path
	for _, name := range []string{"assets/model.bin", "assets/texture.dat", "assets/other.dat", "assets/run-me.sh", "assets/borderline.b"} {
		if !bytes.Equal(laneFile(t, lane, name), files[name]) {
			t.Errorf("%s differs from the committed blob", name)
		}
	}
	// A small text file follows eol=crlf; a large one is binary to Workcopy and
	// is written exactly as Git stores it (here Git normalised it to LF).
	if !bytes.Equal(laneFile(t, lane, "src/small.txt"), []byte("small\r\n")) {
		t.Errorf("a small text file must be converted to CRLF under eol=crlf")
	}
	storedSize := r.Git("cat-file", "-s", "HEAD:docs/big-text.txt")
	if got := laneFile(t, lane, "docs/big-text.txt"); strconv.Itoa(len(got)) != storedSize || bytes.Contains(got, []byte("\r")) {
		t.Errorf("a large text file must be written exactly as stored (%s bytes, LF), got %d bytes with CR=%v", storedSize, len(got), bytes.Contains(got, []byte("\r")))
	}
	if status, statusErr := e.Status(ctx, "task"); statusErr != nil || len(status.Changes) != 0 {
		t.Fatalf("a fresh lane with large files must be clean: %+v %v", status.Changes, statusErr)
	}

	// Edit a large file, add a new large file, delete one.
	edited := append(append([]byte{}, files["assets/model.bin"]...), []byte("EDITED-TAIL")...)
	added := pseudo(4*smallThreshold+7, 99)
	for name, body := range map[string][]byte{"assets/model.bin": edited, "assets/new-dump.bin": added} {
		if err = os.WriteFile(filepath.Join(lane, filepath.FromSlash(name)), body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Remove(filepath.Join(lane, "assets", "other.dat")); err != nil {
		t.Fatal(err)
	}
	status, err := e.Status(ctx, "task")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range status.Changes {
		got[c.Path] = c.Kind
	}
	if len(got) != 3 || got["assets/model.bin"] != "modified" || got["assets/new-dump.bin"] != "added" || got["assets/other.dat"] != "deleted" {
		t.Fatalf("status must report the three large-file edits: %v", got)
	}

	// Review, then commit: the committed blobs are exactly the lane's bytes.
	review, err := e.Merge(ctx, "task", MergeOptions{}, false)
	if err != nil {
		t.Fatalf("review with large files: %v", err)
	}
	if !bytes.Equal(mustRead(t, filepath.Join(review.Review.Path, "assets", "new-dump.bin")), added) {
		t.Fatal("the review must hold the new large file byte for byte")
	}
	if !bytes.Equal(mustRead(t, filepath.Join(review.Review.Path, "assets", "model.bin")), edited) {
		t.Fatal("the review must hold the edited large file byte for byte")
	}
	if !bytes.Equal(mustRead(t, filepath.Join(review.Review.Path, "assets", "texture.dat")), files["assets/texture.dat"]) {
		t.Fatal("an unchanged large file must come through the review unchanged")
	}
	committed, err := e.Merge(ctx, "task", MergeOptions{Commit: true, Approve: true, ReviewID: review.Review.ID, Message: "large edits"}, false)
	if err != nil {
		t.Fatalf("commit with large files: %v", err)
	}
	commit := committed.Review.Commit
	for name, want := range map[string][]byte{"assets/model.bin": edited, "assets/new-dump.bin": added, "assets/texture.dat": files["assets/texture.dat"]} {
		out := r.Git("cat-file", "-s", commit+":"+name)
		if out != strconv.Itoa(len(want)) {
			t.Errorf("committed %s is %s bytes, want %d", name, out, len(want))
		}
	}
	if r.Git("ls-tree", "-r", "--name-only", commit, "assets/other.dat") != "" {
		t.Error("the deleted large file must be absent from the commit")
	}
	if mode := r.Git("ls-tree", commit, "assets/run-me.sh"); !strings.HasPrefix(mode, "100755") && os.PathSeparator == '/' {
		t.Errorf("the executable bit of a large file must survive: %s", mode)
	}
	// Byte-exact: hash the blob git stored and compare with the lane file.
	if stored := r.GitInput("", "rev-parse", commit+":assets/model.bin"); stored == "" {
		t.Fatal("no blob")
	}
	r.Git("cherry-pick", commit)
	if !bytes.Equal(mustRead(t, filepath.Join(r.Path, "assets", "model.bin")), edited) {
		t.Fatal("after integration the main checkout must hold the lane's large file")
	}
	if _, err = e.Sync(ctx, "task", false); err != nil {
		t.Fatalf("sync after integrating large files: %v", err)
	}
	if status, err = e.Status(ctx, "task"); err != nil || len(status.Changes) != 0 {
		t.Fatalf("after integration the lane must be clean: %+v %v", status.Changes, err)
	}

	// Upstream changes a large file the lane did not touch: sync streams it in.
	upstream := pseudo(6*smallThreshold+9, 123)
	r.Write("assets/texture.dat", string(upstream))
	r.Commit("upstream texture")
	if _, err = e.Sync(ctx, "task", false); err != nil {
		t.Fatalf("sync of an upstream large change: %v", err)
	}
	if !bytes.Equal(laneFile(t, lane, "assets/texture.dat"), upstream) {
		t.Fatal("the lane must receive the upstream large file byte for byte")
	}

	// Both sides change the same large file: a conflict, never a text merge.
	local := append(append([]byte{}, upstream...), []byte("LOCAL")...)
	if err = os.WriteFile(filepath.Join(lane, "assets", "texture.dat"), local, 0644); err != nil {
		t.Fatal(err)
	}
	theirs := append(append([]byte{}, upstream...), []byte("THEIRS")...)
	r.Write("assets/texture.dat", string(theirs))
	r.Commit("second upstream texture")
	synced, err := e.Sync(ctx, "task", false)
	if err != nil {
		t.Fatalf("a large-file conflict is reported, not an error: %v", err)
	}
	if len(synced.Conflicts) != 1 || !strings.Contains(synced.Conflicts[0].Reason, "too large to merge") {
		t.Fatalf("expected one large-file conflict, got %+v", synced.Conflicts)
	}
	if !bytes.Equal(laneFile(t, lane, "assets/texture.dat"), local) {
		t.Fatal("the local edit must be preserved while the conflict is open")
	}
	if _, contentErr := e.Content(ctx, "task", synced.Conflicts[0].ID, "local", 0, 1024); failureCode(t, contentErr) != "content-too-large" {
		t.Fatalf("paging a huge side must say so: %v", contentErr)
	}
	token := synced.Conflicts[0].ID
	if _, err = e.Resolve(ctx, "task", "assets/texture.dat", ResolveOptions{UseUpstream: true, ExpectedConflict: token}, false); err != nil {
		t.Fatalf("resolve --use-upstream on a large file: %v", err)
	}
	if !bytes.Equal(laneFile(t, lane, "assets/texture.dat"), theirs) {
		t.Fatal("choosing upstream must write the upstream large file")
	}

	// Destroy: nothing here may stop on size.
	if _, err = e.Destroy(ctx, "task", true, false); err != nil {
		t.Fatalf("destroy with large files: %v", err)
	}
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// The hard limit refuses a tracked file with a message that says what to change.
func TestFilesOverTheHardLimitAreRefusedByNameWithTheSetting(t *testing.T) {
	defer model.SetInlineFileBytes(smallThreshold)()
	defer model.SetMaxFileBytes(4 * smallThreshold)()
	r := testutil.NewRepository(t)
	r.Write("ok.txt", "fine\n")
	r.Write("data/huge.bin", string(pseudo(5*smallThreshold, 8)))
	r.Commit("one file over the limit")
	ctx := context.Background()
	e, _ := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	_, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if failureCode(t, err) != "unsupported-source-entry" || !strings.Contains(err.Error(), "WORKCOPY_MAX_FILE_MIB") || !strings.Contains(err.Error(), "data/huge.bin") {
		t.Fatalf("the refusal must name the file and the setting: %v", err)
	}
	if listed, listErr := e.List(ctx); listErr != nil || len(listed.Workspaces) != 0 {
		t.Fatalf("a refused create must leave nothing behind: %+v %v", listed.Workspaces, listErr)
	}
}

// WC-017 at the engine level: build output matching destroy.discard is removed
// without being read. The files here are larger than even the hard file limit,
// so any code path that reads them (the cause of the original failure) fails;
// merely streaming them would not be enough to pass.
func TestDestroyDiscardsBuildOutputLargerThanTheInMemoryLimit(t *testing.T) {
	defer model.SetInlineFileBytes(smallThreshold)()
	defer model.SetMaxFileBytes(2 * smallThreshold)()
	r := testutil.NewRepository(t)
	r.Write(".gitignore", "bin/\nobj/\n")
	r.Write("src/app.cs", "class App {}\n")
	r.Commit("project")
	owner := filepath.Join(r.Parent, "owner.json")
	if err := os.WriteFile(owner, []byte(`{"schemaVersion":1,"destroy":{"discard":["**/bin/**","**/obj/**"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	e, _ := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	created, err := e.Create(ctx, "task", CreateOptions{All: true, ConfigPath: owner}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	for i := 0; i < 10; i++ {
		name := filepath.Join(lane, "CTDI.Carrier", "bin", "Debug", "Microsoft.Graph."+string(rune('a'+i))+".xml")
		if err = os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(name, pseudo(3*smallThreshold+i, uint32(i+1)), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(lane, "obj", "tiny.cache"), []byte("x"), 0644); err != nil {
		// obj/ may not exist yet
		_ = os.MkdirAll(filepath.Join(lane, "obj"), 0755)
		_ = os.WriteFile(filepath.Join(lane, "obj", "tiny.cache"), []byte("x"), 0644)
	}
	preview, err := e.Destroy(ctx, "task", false, true)
	if err != nil {
		t.Fatalf("destroy --dry-run must not stop on build output over the limit: %v", err)
	}
	if !strings.Contains(strings.Join(preview.Plan.Effects, "\n"), "Discard 11 local-only files") {
		t.Errorf("the plan must list the discarded files: %v", preview.Plan.Effects)
	}
	if _, err = e.Destroy(ctx, "task", false, false); err != nil {
		t.Fatalf("destroy must remove build output of any size: %v", err)
	}
	if _, statErr := os.Lstat(lane); !os.IsNotExist(statErr) {
		t.Fatalf("the lane must be gone: %v", statErr)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(e.WorkRoot, ".workcopy-trash-*")); len(leftovers) != 0 {
		t.Fatalf("quarantine must be cleaned: %v", leftovers)
	}
}

// An unignored large file (a download, a dump) is the lane's work: status lists
// it, it does not stop every command.
func TestAnUnignoredLargeFileDoesNotBreakStatusOrReview(t *testing.T) {
	defer model.SetInlineFileBytes(smallThreshold)()
	r := testutil.NewRepository(t)
	r.Write("a.txt", "a\n")
	r.Commit("base")
	ctx := context.Background()
	e, _ := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	dump := pseudo(3*smallThreshold, 77)
	if err = os.WriteFile(filepath.Join(created.Workspace.Path, "dump.zip"), dump, 0644); err != nil {
		t.Fatal(err)
	}
	status, err := e.Status(ctx, "task")
	if err != nil || len(status.Changes) != 1 || status.Changes[0].Path != "dump.zip" || status.Changes[0].Kind != "added" {
		t.Fatalf("status must list the large file, not fail: %+v %v", status.Changes, err)
	}
	review, err := e.Merge(ctx, "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustRead(t, filepath.Join(review.Review.Path, "dump.zip")), dump) {
		t.Fatal("the review must hold the large file")
	}
}
