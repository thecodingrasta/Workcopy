package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/testutil"
)

// crlfRepository has two projects; every .cs and .sln file is CRLF in a checkout.
func crlfRepository(t *testing.T) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write(".gitattributes", "*.cs text eol=crlf\n*.sln text eol=crlf\n*.csproj text eol=lf\n")
	r.Write("A/A.csproj", "<Project/>\n")
	r.Write("A/A.cs", "class A {}\n")
	r.Write("B/B.csproj", "<Project/>\n")
	r.Write("B/B.cs", "class B {}\nclass B2 {}\n")
	r.Write("Repo.sln", "Microsoft Visual Studio Solution File, Format Version 12.00\nProject(\"{A}\") = \"A\", \"A\\A.csproj\", \"{B}\"\nEndProject\nProject(\"{A}\") = \"B\", \"B\\B.csproj\", \"{C}\"\nEndProject\n")
	r.Commit("base")
	return r
}

func isCRLF(body []byte) bool {
	return bytes.Contains(body, []byte("\n")) && bytes.Count(body, []byte("\n")) == bytes.Count(body, []byte("\r\n"))
}

// expand and sync write files too; they must follow the same rules as create.
func TestExpandAndSyncWriteFilesInTheRepositoryForm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := crlfRepository(t)
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(ctx, "task", CreateOptions{Projects: []string{"A"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	if _, err = e.Expand(ctx, "task", []string{"B"}, false); err != nil {
		t.Fatal(err)
	}
	if !isCRLF(readLane(t, lane, "B/B.cs")) || bytes.Contains(readLane(t, lane, "B/B.csproj"), []byte("\r")) {
		t.Fatal("expanded files must follow eol=crlf and eol=lf")
	}
	// Upstream adds one file and edits another; sync writes both in the right form.
	r.Write("B/New.cs", "class New {}\nclass More {}\n")
	r.Write("A/A.cs", "class A {}\nclass A2 {}\n")
	r.Git("add", "B/New.cs", "A/A.cs")
	r.Git("commit", "-m", "upstream")
	synced, err := e.Sync(ctx, "task", false)
	if err != nil || synced.ExitCode != 0 {
		t.Fatalf("sync: %+v %v", synced, err)
	}
	if !isCRLF(readLane(t, lane, "B/New.cs")) || !isCRLF(readLane(t, lane, "A/A.cs")) || !strings.Contains(string(readLane(t, lane, "A/A.cs")), "A2") {
		t.Fatal("synced files must be written as CRLF")
	}
	if status, statusErr := e.Status(ctx, "task"); statusErr != nil || len(status.Changes) != 0 {
		t.Fatalf("nothing local changed: %+v %v", status.Changes, statusErr)
	}
}

// core.autocrlf=true with no attributes: the whole review/commit path still
// carries one changed line, not a rewritten file.
func TestAutoCRLFRepositoryCommitsOnlyTheRealChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := testutil.NewRepository(t)
	r.Write("notes.txt", numbered("note", 30))
	r.Commit("base")
	base := r.Git("rev-parse", "HEAD")
	r.Git("config", "core.autocrlf", "true")
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !isCRLF(readLane(t, created.Workspace.Path, "notes.txt")) {
		t.Fatal("core.autocrlf=true must give a CRLF lane")
	}
	writeLane(t, created.Workspace.Path, "notes.txt", toCRLF(strings.Replace(numbered("note", 30), "note line 9\n", "note line 9 edited\n", 1)))
	review, err := e.Merge(ctx, "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := e.Merge(ctx, "task", MergeOptions{Commit: true, Approve: true, ReviewID: review.Review.ID, Message: "edit"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if stat := r.Git("diff", "--numstat", base, committed.Review.Commit); !strings.HasPrefix(stat, "1\t1\tnotes.txt") {
		t.Fatalf("one changed line expected, got %q", stat)
	}
	if strings.Contains(r.Git("show", committed.Review.Commit+":notes.txt"), "\r") {
		t.Fatal("history must stay LF")
	}
}

// A dependency fingerprint must not change because an editor changed line
// endings of a lock or project file.
func TestSharedFingerprintIgnoresLineEndingRewrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := testutil.NewRepository(t)
	r.Write(".gitattributes", "*.json text eol=crlf\n")
	r.Write("package.json", "{\n  \"name\": \"x\"\n}\n")
	r.Write("src/a.txt", "a\n")
	r.Commit("base")
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(r.Parent, "packages-cache")
	if err = os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Share(ctx, "task", "node_modules", target, "lock-1", true, false); err != nil {
		t.Skipf("directory links are unavailable here: %v", err)
	}
	if warnings, warnErr := e.SharedWarnings(ctx, "task"); warnErr != nil || len(warnings) != 0 {
		t.Fatalf("fresh mapping: %v %v", warnings, warnErr)
	}
	writeLane(t, created.Workspace.Path, "package.json", "{\n  \"name\": \"x\"\n}\n") // LF where CRLF is expected
	if warnings, warnErr := e.SharedWarnings(ctx, "task"); warnErr != nil || len(warnings) != 0 {
		t.Fatalf("a line-ending rewrite must not make the mapping look stale: %v %v", warnings, warnErr)
	}
	writeLane(t, created.Workspace.Path, "package.json", "{\n  \"name\": \"changed\"\n}\n")
	if warnings, _ := e.SharedWarnings(ctx, "task"); len(warnings) == 0 {
		t.Fatal("a real change to the manifest must still be reported")
	}
}

// The solution filter is generated from a CRLF .sln without altering it.
func TestSolutionFilterFromACRLFSolution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := crlfRepository(t)
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(ctx, "task", CreateOptions{Projects: []string{"A"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !isCRLF(readLane(t, created.Workspace.Path, "Repo.sln")) {
		t.Fatal("precondition: the solution is CRLF in the lane")
	}
	before := append([]byte(nil), readLane(t, created.Workspace.Path, "Repo.sln")...)
	report, err := e.Solution(ctx, "task", "Repo.sln", false)
	if err != nil {
		t.Fatal(err)
	}
	filter := string(readLane(t, created.Workspace.Path, report.Plan.Files[0]))
	if !strings.Contains(filter, `A\\A.csproj`) || strings.Contains(filter, "B.csproj") {
		t.Fatalf("filter: %s", filter)
	}
	if !bytes.Equal(before, readLane(t, created.Workspace.Path, "Repo.sln")) {
		t.Fatal("generating a filter must not rewrite the solution")
	}
	if status, statusErr := e.Status(ctx, "task"); statusErr != nil || len(status.Changes) != 0 {
		t.Fatalf("the filter lives under .workcopy and is not a change: %+v %v", status.Changes, statusErr)
	}
}

// Conflict content is always the stored LF form, so hashes agree across tools,
// and a resolution file with the editor's endings is stored normalised.
func TestConflictContentAndFileResolutionUseTheStoredForm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := crlfRepository(t)
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	writeLane(t, lane, "A/A.cs", "class A { int local; }\r\n")
	r.Write("A/A.cs", "class A { int upstream; }\n")
	r.Git("add", "A/A.cs")
	r.Git("commit", "-m", "conflicting upstream")
	synced, err := e.Sync(ctx, "task", false)
	if err != nil || len(synced.Conflicts) != 1 {
		t.Fatalf("expected one conflict: %+v %v", synced, err)
	}
	conflict := synced.Conflicts[0]
	local, err := e.Content(ctx, "task", conflict.ID, "local", 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := base64.StdEncoding.DecodeString(local.Content.Data)
	if string(body) != "class A { int local; }\n" {
		t.Fatalf("content must be the LF form, got %q", body)
	}
	resolution := filepath.Join(r.Parent, "resolved.cs")
	if err = os.WriteFile(resolution, []byte("class A { int merged; }\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Resolve(ctx, "task", "A/A.cs", ResolveOptions{FromFile: resolution, ExpectedConflict: conflict.ID, ExpectedLocal: conflict.Local.Hash}, false); err != nil {
		t.Fatal(err)
	}
	if got := readLane(t, lane, "A/A.cs"); string(got) != "class A { int merged; }\r\n" {
		t.Fatalf("the lane file keeps the repository's CRLF form: %q", got)
	}
	status, err := e.Status(ctx, "task")
	if err != nil || len(status.Changes) != 1 || status.Changes[0].Path != "A/A.cs" || len(status.Conflicts) != 0 {
		t.Fatalf("the resolution is one contribution: %+v %v", status, err)
	}
}
