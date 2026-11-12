package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

// run executes the real binary and returns stdout, stderr and the exit code.
func run(t *testing.T, directory string, environment []string, arguments ...string) (string, string, int) {
	t.Helper()
	recordCommand(arguments...)
	// Generous: a large repository on a 9p mount (WSL drvfs) takes over a minute per
	// command when the machine is busy. This only bounds a hang.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), environment...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	code := 0
	if err := command.Run(); err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func errorCode(t *testing.T, stdout string) string {
	t.Helper()
	var envelope struct {
		Error *struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || envelope.Error == nil {
		t.Fatalf("expected a JSON error envelope: %v: %s", err, stdout)
	}
	return envelope.Error.Code
}

func numberedLines(prefix string, count int) string {
	var body strings.Builder
	for n := 1; n <= count; n++ {
		fmt.Fprintf(&body, "%s line %d\n", prefix, n)
	}
	return body.String()
}

// TestOwnerReportedIssuesThroughTheRealBinary replays, as one lane lifecycle on
// the shipped executable, the behaviours WORKCOPY-ISSUES.md reported: line
// endings that follow the repository (WC-005), the list branch (WC-009), the
// slim merge output (WC-013), a clear message for the other OS's paths
// (WC-012), the Git program override (WC-002), and a lane deleted outside
// Workcopy (WC-015). Run against v0.1.3 it fails first at the list-branch check (WC-009).
func TestOwnerReportedIssuesThroughTheRealBinary(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("app/Big.cs", numberedLines("cs", 40))
	r.Write("README.md", "readme\n")
	r.Commit("content")
	r.Write(".gitattributes", "*.cs text eol=crlf\n")
	r.Git("add", ".gitattributes")
	r.Git("commit", "-m", "attributes")
	root := filepath.Join(r.Parent, "lanes")

	out, _, code := run(t, r.Path, nil, "list", "--root", root, "--json")
	if code != 0 {
		t.Fatalf("list on an empty root: %d %s", code, out)
	}
	out, _, code = run(t, r.Path, nil, "create", "l1", "--all", "--root", root, "--json")
	if code != 0 {
		t.Fatalf("create: %d %s", code, out)
	}
	var created workspace.Report
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path

	// WC-009: list reports the branch.
	out, _, _ = run(t, r.Path, nil, "list", "--root", root, "--json")
	var listed workspace.Report
	if err := json.Unmarshal([]byte(out), &listed); err != nil || len(listed.Workspaces) != 1 || listed.Workspaces[0].Branch != "refs/heads/main" || listed.Workspaces[0].Phase != "ready" {
		t.Fatalf("WC-009: list must report the branch: %v %s", err, out)
	}

	// WC-005 (a): the lane follows eol=crlf.
	body, err := os.ReadFile(filepath.Join(lane, "app", "Big.cs"))
	if err != nil || !bytes.Contains(body, []byte("\r\n")) || bytes.Count(body, []byte("\n")) != bytes.Count(body, []byte("\r\n")) {
		t.Fatalf("WC-005: app/Big.cs must be CRLF in the lane: %v %q", err, body[:40])
	}

	// WC-005 (b): an LF rewrite plus an edit, then an unrelated upstream edit.
	edited := strings.Replace(numberedLines("cs", 40), "cs line 5\n", "cs line 5 edited\n", 1)
	if err = os.WriteFile(filepath.Join(lane, "app", "Big.cs"), []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	r.Write("app/Big.cs", strings.Replace(numberedLines("cs", 40), "cs line 35\n", "cs line 35 upstream\n", 1))
	r.Git("add", "app/Big.cs")
	r.Git("commit", "-m", "upstream")
	out, _, code = run(t, lane, nil, "sync", "--json")
	if code != 0 {
		t.Fatalf("WC-005: sync must merge cleanly, got %d %s", code, out)
	}
	merged, _ := os.ReadFile(filepath.Join(lane, "app", "Big.cs"))
	if !bytes.Contains(merged, []byte("cs line 5 edited")) || !bytes.Contains(merged, []byte("cs line 35 upstream")) {
		t.Fatalf("both edits must survive the merge:\n%s", merged)
	}

	// WC-013: merge --json stays small unless the inventory is requested.
	out, _, code = run(t, lane, nil, "merge", "--json")
	if code != 0 {
		t.Fatalf("merge: %d %s", code, out)
	}
	if len(out) > 8<<10 {
		t.Fatalf("WC-013: merge --json is %d bytes", len(out))
	}
	var slim workspace.Report
	if err = json.Unmarshal([]byte(out), &slim); err != nil || slim.Review == nil || len(slim.Review.Files) != 0 {
		t.Fatalf("WC-013: review.files must be omitted: %v %s", err, out)
	}
	reviewPath := slim.Review.Path
	out, _, code = run(t, lane, nil, "merge", "--include-files", "--json")
	var full workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &full) != nil || full.Review == nil || len(full.Review.Files) < 2 {
		t.Fatalf("WC-013: --include-files must list the files: %d %s", code, out)
	}

	// WC-012: a path that cannot exist on this platform gets an actionable error.
	// (WC-022: the other platform's spelling of one that can exist is accepted;
	// see wsl_paths_test.go.)
	foreign, expected := "/home/me/repo", "wsl-path-unreachable"
	if runtime.GOOS != "windows" {
		foreign, expected = `\\server\share\repo`, "windows-path-on-posix"
	}
	out, _, code = run(t, lane, nil, "status", "--repo", foreign, "--json")
	if code != 2 || errorCode(t, out) != expected {
		t.Fatalf("WC-012: want exit 2 %s, got %d %s", expected, code, out)
	}

	// WC-002: doctor reports the Git program and line endings; a bad override
	// fails closed instead of silently using another git.
	out, _, code = run(t, r.Path, nil, "doctor", "--json")
	var doctor workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &doctor) != nil || !strings.Contains(doctor.Message, "Line endings:") || !strings.Contains(doctor.Message, "Git: git version") {
		t.Fatalf("WC-002: doctor report: %d %s", code, out)
	}
	out, _, code = run(t, r.Path, []string{"WORKCOPY_GIT=" + filepath.Join(r.Parent, "no-such-git")}, "doctor", "--json")
	if code != 3 || errorCode(t, out) != "git-unavailable" {
		t.Fatalf("WC-002: a bad WORKCOPY_GIT must fail closed: %d %s", code, out)
	}

	// A second, healthy lane has no unfinished review to abandon.
	if out, _, code = run(t, r.Path, nil, "create", "l2", "--all", "--root", root, "--json"); code != 0 {
		t.Fatalf("create l2: %d %s", code, out)
	}
	if out, _, code = run(t, r.Path, nil, "recover", "l2", "--abandon-review", "--root", root, "--dry-run", "--json"); code != 4 {
		t.Fatalf("WC-004: nothing to abandon should be exit 4, got %d %s", code, out)
	}

	// WC-015: delete the lane behind Workcopy's back.
	if err = os.RemoveAll(lane); err != nil {
		t.Fatal(err)
	}
	out, _, _ = run(t, r.Path, nil, "list", "--root", root, "--json")
	var after workspace.Report
	phases := map[string]string{}
	if err = json.Unmarshal([]byte(out), &after); err != nil {
		t.Fatal(err)
	}
	for _, item := range after.Workspaces {
		phases[item.Name] = item.Phase
	}
	if phases["l1"] != "missing" || phases["l2"] != "ready" {
		t.Fatalf("WC-015: list must flag only l1: %v", phases)
	}
	human, _, _ := run(t, r.Path, nil, "list", "--root", root)
	if !strings.Contains(human, "[missing]") {
		t.Fatalf("WC-015: human list output must say missing:\n%s", human)
	}
	out, _, code = run(t, r.Path, nil, "status", "l1", "--root", root, "--json")
	if code != 3 || errorCode(t, out) != "workspace-missing" {
		t.Fatalf("WC-015: status: %d %s", code, out)
	}
	_, stderr, _ := run(t, r.Path, nil, "status", "l1", "--root", root)
	if !strings.Contains(stderr, filepath.Join(".workcopy-registry", "l1.json")) {
		t.Fatalf("WC-015: status must name the registry file:\n%s", stderr)
	}
	for _, args := range [][]string{{"recover", "l1", "--root", root, "--dry-run", "--json"}, {"recover", "l1", "--root", root, "--json"}} {
		if out, _, code = run(t, r.Path, nil, args...); code != 3 || errorCode(t, out) != "workspace-missing" {
			t.Fatalf("WC-015: %v: %d %s", args, code, out)
		}
	}
	if out, _, code = run(t, r.Path, nil, "destroy", "l1", "--root", root, "--dry-run", "--json"); code != 0 {
		t.Fatalf("WC-015: destroy preview: %d %s", code, out)
	}
	out, _, code = run(t, r.Path, nil, "destroy", "l1", "--root", root, "--json")
	var cleared workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &cleared) != nil || !strings.Contains(strings.Join(cleared.NextCommands, "\n"), "worktree remove") {
		t.Fatalf("WC-015: destroy must clear and print the worktree command: %d %s", code, out)
	}
	if _, err = os.Stat(reviewPath); err != nil {
		t.Fatal("WC-015: the review worktree must survive")
	}
	if out, _, code = run(t, r.Path, nil, "status", "l2", "--root", root, "--json"); code != 0 {
		t.Fatalf("the neighbouring lane must stay healthy: %d %s", code, out)
	}
}
