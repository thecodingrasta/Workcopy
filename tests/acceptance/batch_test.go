package acceptance

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"workcopy/internal/testutil"
)

func TestLandExactMCPBatchWithDifferentCLIRoot(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("one.txt", "one base\n")
	r.Write("two.txt", "two base\n")
	r.Commit("base")
	base := r.Git("rev-parse", "HEAD")
	root := filepath.Join(r.Parent, "MCP lanes")
	cliRoot := filepath.Join(r.Parent, "CLI lanes")
	if err := os.Mkdir(cliRoot, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKCOPY_ROOT", cliRoot)
	for _, name := range []string{"one", "two"} {
		created := invoke(t, r.Path, 0, "create", name, "--all", "--root", root)
		if err := os.WriteFile(filepath.Join(created.Workspace.Path, name+".txt"), []byte(name+" reviewed\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	session := startMCP(t, r.Path, root)
	out := mcpCall(t, session, "batch_review", map[string]any{"names": []string{"one", "two"}, "dryRun": false}, 0)
	batch := out.Report.Batch
	if batch == nil || batch.State != "prepared" || batch.Base != base || r.Git("rev-parse", "HEAD") != base {
		t.Fatalf("expected an isolated prepared review, got %+v", batch)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if out.Scope == nil || out.Scope.Root != canonicalRoot {
		t.Fatalf("incorrect review scope: %+v", out.Scope)
	}
	args := []string{"batch-land", batch.ID, "--repo", out.Scope.Repo, "--root", out.Scope.Root}
	if !reflect.DeepEqual(out.BatchLandArgs, args) {
		t.Fatalf("handoff arguments = %q, want %q", out.BatchLandArgs, args)
	}
	invoke(t, r.Path, 2, out.BatchLandArgs...)
	if r.Git("rev-parse", "HEAD") != base {
		t.Fatal("unapproved handoff changed HEAD")
	}
	invoke(t, r.Path, 0, append(out.BatchLandArgs, "--approve")...)
	if r.Git("rev-parse", "HEAD") != batch.Commit || r.Git("rev-parse", "HEAD^") != base {
		t.Fatal("CLI did not land the exact MCP review")
	}
	if entries, err := os.ReadDir(cliRoot); err != nil || len(entries) != 0 {
		t.Fatalf("landing touched CLI default root: %v, %v", entries, err)
	}
}

func TestBatchReviewsAndLandsOnlySelectedLanes(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("shared.txt", "base\n")
	r.Write("private.txt", "original\n")
	r.Write(".gitignore", "ignored.txt\n")
	r.Commit("base")
	root := filepath.Join(r.Parent, "lanes")
	names := []string{"first", "second", "third", "excluded"}
	for _, name := range names {
		created := invoke(t, r.Path, 0, "create", name, "--all", "--root", root)
		lane := created.Workspace.Path
		file, content := name+".txt", name+" contribution\n"
		if name == "first" || name == "second" {
			file = "shared.txt"
		}
		if err := os.WriteFile(filepath.Join(lane, file), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if name == "third" {
			if err := os.WriteFile(filepath.Join(lane, "ignored.txt"), []byte("selected content\n"), 0600); err != nil {
				t.Fatal(err)
			}
			// Force this path to be an ordinary contribution in the lane.
			if err := os.WriteFile(filepath.Join(lane, ".gitignore"), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	r.Write("upstream.txt", "new upstream file\n")
	r.Commit("advance target")
	base := r.Git("rev-parse", "HEAD")
	r.Write("private.txt", "staged bytes\n")
	r.Git("add", "private.txt")
	r.Write("private.txt", "unstaged bytes\n")
	r.Write("private-untracked.txt", "untracked bytes\n")
	r.Write("ignored.txt", "private ignored bytes\n")
	selected := names[:3]
	session := startMCP(t, r.Path, root)
	request := map[string]any{"names": selected, "branch": "main", "dryRun": false}
	conflict := mcpCall(t, session, "batch_review", request, 1).Report.BatchConflict
	if conflict == nil || conflict.Path != "shared.txt" || conflict.Lane != "second" {
		t.Fatalf("unexpected conflict: %+v", conflict)
	}
	body, err := json.Marshal(map[string]any{"resolutions": []map[string]string{{
		"token": conflict.Token, "choice": "file", "content": base64.StdEncoding.EncodeToString([]byte("combined contribution\n")),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	request["resolutions"] = []map[string]string{{
		"token": conflict.Token, "choice": "file", "content": base64.StdEncoding.EncodeToString([]byte("combined contribution\n")),
	}}
	mcpBatch := mcpCall(t, session, "batch_review", request, 0).Report.Batch
	if mcpBatch == nil || mcpBatch.State != "prepared" || mcpBatch.Base != base {
		t.Fatalf("unexpected resolved MCP review: %+v", mcpBatch)
	}
	resolution := filepath.Join(r.Parent, "resolution.json")
	if err = os.WriteFile(resolution, body, 0600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"batch-review", "--root", root, "--branch", "main", "--from-file", resolution}, selected...)
	report := invoke(t, r.Path, 0, args...)
	batch := report.Batch
	if batch == nil || batch.Base != base || !reflect.DeepEqual(batch.Lanes, selected) || batch.State != "prepared" {
		t.Fatalf("unexpected batch: %+v", batch)
	}
	if r.Git("rev-parse", "HEAD") != base {
		t.Fatal("review changed the target")
	}
	invoke(t, r.Path, 2, "batch-land", batch.ID, "--root", root)
	invoke(t, r.Path, 3, "batch-land", batch.ID, "--root", root, "--approve")
	if r.Git("rev-parse", "HEAD") != base || r.Git("status", "--short", "--", "private.txt") != "MM private.txt" {
		t.Fatal("collision changed branch or local edits")
	}
	if got, readErr := os.ReadFile(filepath.Join(r.Path, "ignored.txt")); readErr != nil || string(got) != "private ignored bytes\n" {
		t.Fatalf("ignored file overwritten: %q %v", got, readErr)
	}
	if err = os.Remove(filepath.Join(r.Path, "ignored.txt")); err != nil {
		t.Fatal(err)
	}
	invoke(t, r.Path, 0, "batch-land", batch.ID, "--root", root, "--approve", "--dry-run")
	invoke(t, r.Path, 0, "batch-land", batch.ID, "--root", root, "--approve")
	invoke(t, r.Path, 0, "batch-land", batch.ID, "--root", root, "--approve")
	invoke(t, r.Path, 4, "batch-recover", batch.ID, "--root", root)
	if r.Git("rev-parse", "HEAD") != batch.Commit || r.Git("rev-parse", "HEAD^") != base {
		t.Fatal("batch was not landed as one commit")
	}
	for path, want := range map[string]string{
		"shared.txt": "combined contribution", "upstream.txt": "new upstream file",
		"third.txt": "third contribution", "ignored.txt": "selected content", "private.txt": "original",
	} {
		if got := r.Git("show", "HEAD:"+path); got != want {
			t.Fatalf("%s: got %q, want %q", path, got, want)
		}
	}
	for _, file := range []string{"excluded.txt", "private-untracked.txt"} {
		if got := r.Git("ls-tree", "HEAD", "--", file); got != "" {
			t.Fatalf("unexpected committed file: %s", got)
		}
	}
	if got := r.Git("show", ":private.txt"); got != "staged bytes" {
		t.Fatalf("original staging changed: %q", got)
	}
	if got, readErr := os.ReadFile(filepath.Join(r.Path, "private.txt")); readErr != nil || string(got) != "unstaged bytes\n" {
		t.Fatalf("unstaged edit changed: %q %v", got, readErr)
	}
}
