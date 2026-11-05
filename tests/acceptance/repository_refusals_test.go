package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/testutil"
)

// These tests cover what Workcopy must refuse, and how. Every refusal is a
// stable exit code and error code that says what to do, never a raw Git or OS
// message, and it happens before any lane directory, registration or pinned ref
// exists. Where a workaround is offered in the message, the test proves it.

func errorOf(t *testing.T, stdout string) (string, string) {
	t.Helper()
	var envelope struct {
		Error *struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || envelope.Error == nil {
		t.Fatalf("expected a JSON error envelope: %v: %q", err, stdout)
	}
	return envelope.Error.Code, envelope.Error.Message
}

// assertNothingCreated proves a refused command left no lane, registration or
// pinned ref. The lane root may exist (Workcopy locks it first) but must hold
// no lane directory and no registration.
func assertNothingCreated(t *testing.T, repository, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != ".workcopy-registry" {
			t.Errorf("refused command left %s in the lane root", entry.Name())
		}
	}
	registrations, _ := filepath.Glob(filepath.Join(root, ".workcopy-registry", "*.json"))
	if len(registrations) != 0 {
		t.Errorf("refused command left registrations: %v", registrations)
	}
	if repository != "" {
		if pins := gitText(t, repository, "for-each-ref", "refs/workcopy"); pins != "" {
			t.Errorf("refused command left pinned refs:\n%s", pins)
		}
	}
}

func TestUnusableRepositoriesAreRefusedWithAnActionableError(t *testing.T) {
	t.Parallel()
	complete := testutil.NewRepository(t)
	plainFiles(complete)
	complete.Commit("base")
	bare := filepath.Join(complete.Parent, "bare.git")
	isolatedGit(t, complete.Parent, nil, "clone", "--bare", "-q", complete.Path, bare)
	unborn := testutil.NewRepository(t)
	notRepository := filepath.Join(complete.Parent, "just a folder")
	if err := os.Mkdir(notRepository, 0755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(complete.Parent, "lanes")

	cases := []struct {
		name       string
		directory  string
		repository string // for the leftover-pin check; empty when there is none
		arguments  []string
		exit       int
		code       string
		mention    string
	}{
		{"repository with no commits", unborn.Path, unborn.Path, []string{"create", "l1", "--all"}, 3, "no-commits", "no commits"},
		{"discover in a repository with no commits", unborn.Path, unborn.Path, []string{"discover"}, 3, "no-commits", "no commits"},
		{"bare repository", bare, "", []string{"create", "l1", "--all"}, 3, "bare-repository", "bare"},
		{"list in a bare repository", bare, "", []string{"list"}, 3, "bare-repository", "bare"},
		{"directory that is not a repository", notRepository, "", []string{"create", "l1", "--all"}, 3, "not-a-repository", "--repo"},
		{"list outside any repository", notRepository, "", []string{"list"}, 3, "not-a-repository", "--repo"},
		{"unknown branch", complete.Path, complete.Path, []string{"create", "l1", "--all", "--branch", "no-such-branch"}, 2, "ref-not-found", "no-such-branch"},
	}
	for _, c := range cases {
		stdout, stderr, code := run(t, c.directory, nil, append(append([]string{}, c.arguments...), "--root", root, "--json")...)
		if code != c.exit {
			t.Errorf("%s: want exit %d, got %d: %s %s", c.name, c.exit, code, stdout, stderr)
			continue
		}
		errorCodeValue, message := errorOf(t, stdout)
		if errorCodeValue != c.code || !strings.Contains(message, c.mention) || strings.Contains(message, "rev-parse") || strings.Contains(message, "exit status") {
			t.Errorf("%s: want clear %q mentioning %q, got %q: %s", c.name, c.code, c.mention, errorCodeValue, message)
		}
		assertNothingCreated(t, c.repository, root)
	}
}

func TestSubmodulesAreRefusedUnlessLeftOutOfTheSelection(t *testing.T) {
	t.Parallel()
	library := testutil.NewRepository(t)
	library.Write("lib.txt", "library\n")
	library.Commit("library")
	r := testutil.NewRepository(t)
	r.Write(".workcopy.json", `{"schemaVersion":1,"projects":[{"id":"app","path":"app"}]}`)
	r.Write("app/main.txt", "main\n")
	r.Commit("app")
	r.Git("-c", "protocol.file.allow=always", "submodule", "add", "-q", library.Path, "vendor/library")
	r.Git("commit", "-q", "-m", "add submodule")
	root := filepath.Join(r.Parent, "lanes")

	stdout, _, code := run(t, r.Path, nil, "create", "everything", "--all", "--root", root, "--json")
	errorCodeValue, message := errorOf(t, stdout)
	if code != 3 || errorCodeValue != "unsupported-source-entry" || !strings.Contains(message, "submodule") || !strings.Contains(message, "vendor/library") || !strings.Contains(message, "--project") {
		t.Fatalf("submodule refusal must name the path and the workaround: %d %s", code, stdout)
	}
	assertNothingCreated(t, r.Path, root)

	created := laneReport(t, r.Path, nil, 0, "create", "app-only", "--project", "app", "--root", root)
	assertLaneMirrorsCommitSubset(t, created.Workspace.Path, map[string]string{"app/main.txt": "main\n", ".workcopy.json": "*", ".gitmodules": "*"})
	laneReport(t, created.Workspace.Path, nil, 0, "destroy")
}

// assertLaneMirrorsCommitSubset checks a lane that holds only part of a commit.
// The value "*" accepts any content for a file whose bytes are not the point.
func assertLaneMirrorsCommitSubset(t *testing.T, lane string, expected map[string]string) {
	t.Helper()
	seen := 0
	_ = filepath.WalkDir(lane, func(path string, entry os.DirEntry, err error) error {
		relative, _ := filepath.Rel(lane, path)
		relative = filepath.ToSlash(relative)
		if err != nil || relative == ".workcopy" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		want, ok := expected[relative]
		body, _ := os.ReadFile(path)
		if !ok || (want != "*" && string(body) != want) {
			t.Errorf("unexpected or wrong lane file %s", relative)
		}
		seen++
		return nil
	})
	if seen != len(expected) {
		t.Errorf("lane holds %d files, want %d", seen, len(expected))
	}
}

func TestUnportableTrackedPathsAreRefusedBeforeAnythingIsWritten(t *testing.T) {
	t.Parallel()
	bad := []string{
		"con.txt", "aux", "NUL.md", "lpt1", "a:b.txt", "trail.", "dir./x.txt", "sp ace /x.txt",
		"q?.txt", "star*.txt", "lt<.txt", "pipe|.txt", `back\slash.txt`,
		".git/hooks/post-checkout", "nested/.GIT/config", "GIT~1/config", "nested/.workcopy/state",
	}
	for _, name := range bad {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := testutil.NewRepository(t)
			commitRawEntries(t, r, []rawEntry{
				{"100644", ".workcopy.json", `{"schemaVersion":1,"projects":[{"id":"ok","path":"ok"}]}`},
				{"100644", "ok/fine.txt", "fine\n"},
				{"100644", "bad/" + name, "must never be written\n"},
			}, false)
			root := filepath.Join(r.Parent, "lanes")
			stdout, _, code := run(t, r.Path, nil, "create", "l1", "--all", "--root", root, "--json")
			errorCodeValue, message := errorOf(t, stdout)
			if code != 3 || errorCodeValue != "unportable-source-path" || !strings.Contains(message, "--project") {
				t.Fatalf("want exit 3 unportable-source-path with the workaround, got %d %s", code, stdout)
			}
			assertNothingCreated(t, r.Path, root)

			// The advertised workaround: select only the portable project.
			created := laneReport(t, r.Path, nil, 0, "create", "l2", "--project", "ok", "--root", root)
			assertLaneMirrorsCommitSubset(t, created.Workspace.Path, map[string]string{"ok/fine.txt": "fine\n", ".workcopy.json": "*"})
			laneReport(t, created.Workspace.Path, nil, 0, "destroy", "--force")
		})
	}
}

func TestCaseOnlyDifferencesAreRefusedOnEveryPlatform(t *testing.T) {
	t.Parallel()
	r := testutil.NewRepository(t)
	commitRawEntries(t, r, []rawEntry{{"100644", "docs/Readme.md", "upper\n"}, {"100644", "docs/README.md", "lower\n"}}, false)
	root := filepath.Join(r.Parent, "lanes")
	stdout, _, code := run(t, r.Path, nil, "create", "l1", "--all", "--root", root, "--json")
	errorCodeValue, message := errorOf(t, stdout)
	if code != 3 || errorCodeValue != "case-collision" || !strings.Contains(message, "Readme.md") || !strings.Contains(message, "README.md") {
		t.Fatalf("case collision must name both paths: %d %s", code, stdout)
	}
	assertNothingCreated(t, r.Path, root)
}

// Unicode normalization forms are distinct names on Linux and Windows but the
// same file on macOS. Either outcome is acceptable; silently losing one file is not.
func TestCanonicallyEquivalentUnicodeNamesNeverOverwriteEachOther(t *testing.T) {
	t.Parallel()
	r := testutil.NewRepository(t)
	precomposed, decomposed := "docs/caf\u00e9.txt", "docs/cafe\u0301.txt"
	commitRawEntries(t, r, []rawEntry{{"100644", "keep.txt", "k\n"}, {"100644", precomposed, "precomposed\n"}, {"100644", decomposed, "decomposed\n"}}, false)
	root := filepath.Join(r.Parent, "lanes")
	stdout, _, code := run(t, r.Path, nil, "create", "l1", "--all", "--root", root, "--json")
	if code == 0 {
		var created struct{ Workspace struct{ Path string } }
		if err := json.Unmarshal([]byte(stdout), &created); err != nil {
			t.Fatal(err)
		}
		lane := created.Workspace.Path
		first, errFirst := os.ReadFile(filepath.Join(lane, filepath.FromSlash(precomposed)))
		second, errSecond := os.ReadFile(filepath.Join(lane, filepath.FromSlash(decomposed)))
		if errFirst != nil || errSecond != nil || string(first) != "precomposed\n" || string(second) != "decomposed\n" {
			t.Fatalf("create reported success but a canonically equivalent name lost its content: %v %v %q %q", errFirst, errSecond, first, second)
		}
		laneReport(t, lane, nil, 0, "destroy", "--force")
		return
	}
	errorCodeValue, _ := errorOf(t, stdout)
	if errorCodeValue == "" {
		t.Fatalf("a refusal must carry an error code: %s", stdout)
	}
	// A refusal may leave recoverable state; the owner must always be able to clear it.
	laneReport(t, r.Path, nil, 0, "destroy", "l1", "--force", "--root", root)
	assertNothingCreated(t, r.Path, root)
}

func TestUnsafeSymbolicLinksAreRefusedBeforeAnythingIsWritten(t *testing.T) {
	t.Parallel()
	for name, target := range map[string]string{
		"parent escape":      "../../outside",
		"absolute posix":     "/etc/passwd",
		"windows drive":      "C:/Windows/System32",
		"backslash":          `..\..\outside`,
		"into git metadata":  ".git/config",
		"reserved device":    "docs/con",
		"through workcopy":   ".workcopy/manifest.json",
		"trailing dot parts": "docs/trail./x",
	} {
		name, target := name, target
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := testutil.NewRepository(t)
			commitRawEntries(t, r, []rawEntry{{"100644", "ok/fine.txt", "fine\n"}, {"120000", "bad/link", target}}, false)
			root := filepath.Join(r.Parent, "lanes")
			stdout, _, code := run(t, r.Path, nil, "create", "l1", "--all", "--root", root, "--json")
			errorCodeValue, message := errorOf(t, stdout)
			if code != 3 || errorCodeValue != "unsafe-source-link" || !strings.Contains(message, "bad/link") {
				t.Fatalf("want exit 3 unsafe-source-link naming the link, got %d %s", code, stdout)
			}
			assertNothingCreated(t, r.Path, root)
		})
	}
}

// Only meaningful where this account cannot create symbolic links (Windows
// without Developer Mode). Elsewhere the shapes test covers links end to end.
func TestTrackedSymbolicLinksWithoutPrivilegeFailBeforeWriting(t *testing.T) {
	if symlinksAvailable(t) {
		t.Skip("this account can create symbolic links; the lane round trip covers them")
	}
	r := testutil.NewRepository(t)
	commitRawEntries(t, r, []rawEntry{
		{"100644", ".workcopy.json", `{"schemaVersion":1,"projects":[{"id":"ok","path":"ok"}]}`},
		{"100644", "ok/fine.txt", "fine\n"},
		{"100644", "linked/target.txt", "t\n"},
		{"120000", "linked/link", "target.txt"},
	}, false)
	root := filepath.Join(r.Parent, "lanes")
	stdout, _, code := run(t, r.Path, nil, "create", "l1", "--all", "--root", root, "--json")
	errorCodeValue, message := errorOf(t, stdout)
	if code != 3 || errorCodeValue != "symlink-unavailable" || !strings.Contains(message, "linked/link") || !strings.Contains(message, "Nothing was created") {
		t.Fatalf("want exit 3 symlink-unavailable naming the link, got %d %s", code, stdout)
	}
	assertNothingCreated(t, r.Path, root)
	created := laneReport(t, r.Path, nil, 0, "create", "l2", "--project", "ok", "--root", root)
	assertLaneMirrorsCommitSubset(t, created.Workspace.Path, map[string]string{"ok/fine.txt": "fine\n", ".workcopy.json": "*"})
	laneReport(t, created.Workspace.Path, nil, 0, "destroy", "--force")
}

func TestLaneNamesCannotEscapeTheRootOrBeNonPortable(t *testing.T) {
	t.Parallel()
	r := testutil.NewRepository(t)
	plainFiles(r)
	r.Commit("base")
	root := filepath.Join(r.Parent, "lanes")
	for _, name := range []string{"..", ".", "../evil", `..\evil`, "a/b", `a\b`, "con", "CON", "nul", "aux.txt", "com1", "lane.", "lane ", "a:b", ".git", ".workcopy-registry", "名前", "with space", strings.Repeat("x", 65), "-", "_lane"} {
		stdout, _, code := run(t, r.Path, nil, "create", name, "--all", "--root", root, "--json")
		if code != 2 {
			t.Errorf("name %q: want exit 2, got %d: %s", name, code, stdout)
			continue
		}
		if errorCodeValue, _ := errorOf(t, stdout); errorCodeValue != "invalid-name" && errorCodeValue != "invalid-arguments" {
			t.Errorf("name %q: want invalid-name, got %q", name, errorCodeValue)
		}
	}
	// Nothing escaped: the only things beside the repository are the lane root and fixture marker.
	entries, _ := os.ReadDir(r.Parent)
	for _, entry := range entries {
		switch entry.Name() {
		case "repository", "lanes", ".workcopy-fixture-owner":
		default:
			t.Errorf("a refused name created %s beside the repository", entry.Name())
		}
	}
	assertNothingCreated(t, r.Path, root)

	// Names that differ only by case are one lane on case-insensitive volumes and
	// two on case-sensitive ones; both must be handled without a raw failure.
	laneReport(t, r.Path, nil, 0, "create", "Lane", "--all", "--root", root)
	stdout, _, code := run(t, r.Path, nil, "create", "lane", "--all", "--root", root, "--json")
	listed := laneReport(t, r.Path, nil, 0, "list", "--root", root)
	switch code {
	case 0:
		if len(listed.Workspaces) != 2 {
			t.Fatalf("a case-sensitive volume must register both lanes: %+v", listed.Workspaces)
		}
	case 3:
		if errorCodeValue, _ := errorOf(t, stdout); errorCodeValue != "workspace-exists" || len(listed.Workspaces) != 1 {
			t.Fatalf("a case-insensitive volume must refuse the second name cleanly: %s %+v", stdout, listed.Workspaces)
		}
	default:
		t.Fatalf("case-variant lane name failed with exit %d: %s", code, stdout)
	}
	for _, lane := range listed.Workspaces {
		laneReport(t, r.Path, nil, 0, "destroy", lane.Name, "--force", "--root", root)
	}
}

// The review commit needs an author. A fresh machine, container or CI job has
// none until the owner supplies one, either in Git config or in GIT_* variables.
func TestReviewCommitNeedsAnIdentityAndAcceptsOneFromTheEnvironment(t *testing.T) {
	t.Parallel()
	r := testutil.NewRepository(t)
	plainFiles(r)
	r.Commit("base")
	r.Git("config", "--unset", "user.name")
	r.Git("config", "--unset", "user.email")
	r.Git("config", "user.useConfigOnly", "true")
	home := filepath.Join(r.Parent, "empty home")
	if err := os.Mkdir(home, 0755); err != nil {
		t.Fatal(err)
	}
	// Empty values override any identity in the ambient environment, so the
	// first attempt is deterministic wherever the suite runs.
	none := []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home, "EMAIL=", "GIT_AUTHOR_NAME=", "GIT_AUTHOR_EMAIL=", "GIT_COMMITTER_NAME=", "GIT_COMMITTER_EMAIL="}
	root := filepath.Join(r.Parent, "lanes")
	lane := laneReport(t, r.Path, none, 0, "create", "l1", "--all", "--root", root).Workspace.Path
	if err := os.WriteFile(filepath.Join(lane, "src", "app.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	review := laneReport(t, lane, none, 0, "merge")

	stdout, _, code := run(t, lane, none, "merge", "--commit", "--approve", "--review", review.Review.ID, "--message", "Needs an author", "--json")
	errorCodeValue, message := errorOf(t, stdout)
	if code != 3 || errorCodeValue != "git-identity-missing" || !strings.Contains(message, "user.name") || !strings.Contains(message, "GIT_AUTHOR_NAME") {
		t.Fatalf("a missing identity must be a clear exit 3: %d %s", code, stdout)
	}
	if _, _, dryCode := run(t, lane, none, "merge", "--commit", "--approve", "--review", review.Review.ID, "--message", "Needs an author", "--dry-run", "--json"); dryCode != 3 {
		t.Fatalf("a dry run must report the same problem instead of promising a commit, got exit %d", dryCode)
	}

	withIdentity := append(append([]string{}, none...), "GIT_AUTHOR_NAME=Env Author", "GIT_AUTHOR_EMAIL=author@example.invalid", "GIT_COMMITTER_NAME=Env Committer", "GIT_COMMITTER_EMAIL=committer@example.invalid")
	committed := laneReport(t, lane, withIdentity, 0, "merge", "--commit", "--approve", "--review", review.Review.ID, "--message", "Authored from the environment")
	if got := gitText(t, r.Path, "log", "-1", "--format=%an|%ae|%cn|%ce|%s", committed.Review.Commit); got != "Env Author|author@example.invalid|Env Committer|committer@example.invalid|Authored from the environment" {
		t.Fatalf("the commit must carry the environment identity, got %q", got)
	}
	laneReport(t, lane, withIdentity, 3, "destroy")
	laneReport(t, lane, withIdentity, 0, "destroy", "--force")
}

// An agent reaches the same refusals through MCP. The error code and exit code
// must match the CLI so one recovery instruction works for both, and the server
// must stay usable afterwards.
func TestMCPRefusalsMatchTheCommandLine(t *testing.T) {
	t.Parallel()
	good := testutil.NewRepository(t)
	plainFiles(good)
	good.Commit("base")
	unborn := testutil.NewRepository(t)
	bare := filepath.Join(good.Parent, "bare.git")
	isolatedGit(t, good.Parent, nil, "clone", "--bare", "-q", good.Path, bare)
	notRepository := filepath.Join(good.Parent, "just a folder")
	if err := os.Mkdir(notRepository, 0755); err != nil {
		t.Fatal(err)
	}
	reserved := testutil.NewRepository(t)
	commitRawEntries(t, reserved, []rawEntry{{"100644", "ok/fine.txt", "fine\n"}, {"100644", "bad/con.txt", "x\n"}}, false)
	links := testutil.NewRepository(t)
	commitRawEntries(t, links, []rawEntry{{"100644", "ok/fine.txt", "fine\n"}, {"120000", "bad/link", "../../outside"}}, false)

	session := startMCPArgs(t, "--root", filepath.Join(good.Parent, "mcp lanes"), "--max-lanes", "5")
	cases := []struct {
		name, repository, lane, selector string
		exit                             int
		code                             string
	}{
		{"no commits", unborn.Path, "l1", "src", 3, "no-commits"},
		{"bare repository", bare, "l1", "src", 3, "bare-repository"},
		{"not a repository", notRepository, "l1", "src", 3, "not-a-repository"},
		{"unportable tracked path", reserved.Path, "l1", "bad", 3, "unportable-source-path"},
		{"unsafe tracked link", links.Path, "l1", "bad", 3, "unsafe-source-link"},
		{"reserved lane name", good.Path, "nul", "src", 2, "invalid-name"},
		{"lane name escaping the root", good.Path, "../evil", "src", 2, "invalid-name"},
		{"whole repository is not a selector", good.Path, "l1", ".", 2, "invalid-selection"},
		{"selection that matches nothing", good.Path, "l1", "no/such/dir", 2, "project-not-found"},
	}
	for _, c := range cases {
		args := map[string]any{"repo": c.repository, "root": filepath.Join(good.Parent, "mcp lanes", strings.ReplaceAll(c.name, " ", "-")), "name": c.lane, "projects": []string{c.selector}, "dryRun": false}
		out := mcpCall(t, session, "create", args, c.exit)
		if out.Error == nil || out.Error.Code != c.code {
			t.Errorf("%s: MCP must report %q, got %+v", c.name, c.code, out.Error)
		}
	} // A refusal must not poison the session: a good repository still works.
	args := map[string]any{"repo": good.Path, "root": filepath.Join(good.Parent, "mcp lanes", "after"), "name": "after", "projects": []string{"src"}, "dryRun": false}
	created := mcpCall(t, session, "create", args, 0)
	mcpCall(t, session, "destroy", map[string]any{"repo": good.Path, "root": filepath.Join(good.Parent, "mcp lanes", "after"), "name": created.Report.Workspace.Name, "dryRun": false}, 0)
}

// A file over the lane limit is refused by name and size, and the same
// repository still works when the oversized path is left out of the selection.
func TestOversizedFilesAreRefusedUnlessLeftOutOfTheSelection(t *testing.T) {
	t.Parallel()
	r := testutil.NewRepository(t)
	commitRawEntries(t, r, []rawEntry{
		{"100644", ".workcopy.json", `{"schemaVersion":1,"projects":[{"id":"ok","path":"ok"}]}`},
		{"100644", "ok/fine.txt", "fine\n"},
		{"100644", "assets/huge.bin", string(xorshiftBytes(65 << 20))},
	}, false)
	root := filepath.Join(r.Parent, "lanes")
	stdout, _, code := run(t, r.Path, nil, "create", "l1", "--all", "--root", root, "--json")
	errorCodeValue, message := errorOf(t, stdout)
	if code != 3 || errorCodeValue != "unsupported-source-entry" || !strings.Contains(message, "64 MiB") || !strings.Contains(message, "assets/huge.bin") || strings.Contains(message, "submodule") {
		t.Fatalf("an oversized file must be named with the limit and not confused with a submodule: %d %s", code, stdout)
	}
	assertNothingCreated(t, r.Path, root)
	created := laneReport(t, r.Path, nil, 0, "create", "l2", "--project", "ok", "--root", root)
	assertLaneMirrorsCommitSubset(t, created.Workspace.Path, map[string]string{"ok/fine.txt": "fine\n", ".workcopy.json": "*"})
	laneReport(t, created.Workspace.Path, nil, 0, "destroy", "--force")
}
