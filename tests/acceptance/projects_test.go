package acceptance

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"workcopy/internal/testutil"
)

// Several repositories are in daily use at once, each with its own lanes. These
// tests drive the real binary the way a person or agent moving between projects
// does: no wrapper script supplying --repo/--root/--config.

func projectFixture(t *testing.T, name string) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write("README.md", name+"\n")
	r.Write("app/main.txt", name+" main\n")
	r.Write("app/other.txt", name+" other\n")
	r.Write(".gitignore", "out/\n")
	r.Commit(name)
	return r
}

func gitDirOf(t *testing.T, r *testutil.Repository) string {
	t.Helper()
	return filepath.Join(r.Path, ".git")
}

func writeProjectSettings(t *testing.T, r *testutil.Repository, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(gitDirOf(t, r), "workcopy.env"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPathCommandNeedsNoWrapper(t *testing.T) {
	t.Parallel()
	r := projectFixture(t, "alpha")
	root := filepath.Join(r.Parent, "lanes")
	created := laneReport(t, r.Path, nil, 0, "create", "task", "--all", "--root", root)

	// From the main checkout, from inside the lane, and by --workspace.
	for _, c := range []struct {
		directory string
		args      []string
	}{{r.Path, []string{"path", "task", "--root", root}}, {created.Workspace.Path, []string{"path"}}, {r.Parent, []string{"path", "task", "--repo", r.Path, "--root", root}}} {
		stdout, _, code := run(t, c.directory, nil, c.args...)
		if code != 0 || strings.TrimSpace(stdout) != created.Workspace.Path {
			t.Errorf("%v from %s: want the bare lane path %q, got exit %d %q", c.args, c.directory, created.Workspace.Path, code, stdout)
		}
	}
	report := laneReport(t, r.Path, nil, 0, "path", "task", "--root", root)
	if report.Workspace == nil || report.Workspace.Path != created.Workspace.Path || report.Workspace.Phase != "ready" {
		t.Fatalf("path --json: %+v", report.Workspace)
	}

	// An unknown lane is exit 3 with a stable code, not an empty path a script
	// would then cd into.
	stdout, _, code := run(t, r.Path, nil, "path", "nothing", "--root", root, "--json")
	if code != 3 || errorCode(t, stdout) != "workspace-not-registered" {
		t.Fatalf("unknown lane: %d %s", code, stdout)
	}
	if bare, _, bareCode := run(t, r.Path, nil, "path", "nothing", "--root", root); bareCode != 3 || strings.TrimSpace(bare) != "" {
		t.Fatalf("a failed path must print nothing on stdout: %d %q", bareCode, bare)
	}
}

func TestEachRepositoryKeepsItsOwnSettingsAndLanes(t *testing.T) {
	t.Parallel()
	alpha, beta := projectFixture(t, "alpha"), projectFixture(t, "beta")
	alphaLanes := filepath.Join(alpha.Parent, "alpha-lanes")
	alphaConfig := filepath.Join(alpha.Parent, "alpha-owner.json")
	if err := os.WriteFile(alphaConfig, []byte(`{"schemaVersion":1,"projects":[{"id":"alpha-app","path":"app"}],"destroy":{"discard":["out/**"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	writeProjectSettings(t, alpha, "WORKCOPY_ROOT="+alphaLanes+"\nWORKCOPY_CONFIG="+alphaConfig+"\n")

	// Alpha: lane root and owner config come from its own settings file.
	created := laneReport(t, alpha.Path, nil, 0, "create", "task", "--project", "alpha-app")
	if filepath.Dir(created.Workspace.Path) != alphaLanes {
		t.Fatalf("alpha lanes must go to the repository's own root %s, got %s", alphaLanes, created.Workspace.Path)
	}
	// The project id exists only in alpha's owner config, so selecting it proves the
	// config was applied without any --config flag; beta does not know it.
	if stdout, _, code := run(t, beta.Path, nil, "create", "probe", "--project", "alpha-app", "--dry-run", "--json"); code == 0 {
		t.Fatalf("beta must not see alpha's owner config: %s", stdout)
	}
	// Beta has no file: default root, no config, same lane name without conflict.
	other := laneReport(t, beta.Path, nil, 0, "create", "task", "--all")
	if filepath.Dir(other.Workspace.Path) == alphaLanes || other.Workspace.Path == created.Workspace.Path {
		t.Fatalf("beta must not share alpha's root: %s", other.Workspace.Path)
	}

	// Each repository lists only its own lane.
	for _, c := range []struct {
		r    *testutil.Repository
		want string
	}{{alpha, created.Workspace.Path}, {beta, other.Workspace.Path}} {
		listed := laneReport(t, c.r.Path, nil, 0, "list")
		if len(listed.Workspaces) != 1 || listed.Workspaces[0].Path != c.want {
			t.Errorf("list in %s: %+v, want only %s", c.r.Path, listed.Workspaces, c.want)
		}
	}

	// alpha's owner config governs alpha's destroy: out/** is discardable build
	// output there, and in beta (no config) it is ignored local state that
	// blocks cleanup until the owner decides.
	for _, lane := range []string{created.Workspace.Path, other.Workspace.Path} {
		if err := os.MkdirAll(filepath.Join(lane, "out"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lane, "out", "build.dll"), []byte("binary"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	laneReport(t, alpha.Path, nil, 0, "destroy", "task")
	if _, err := os.Stat(created.Workspace.Path); !os.IsNotExist(err) {
		t.Fatalf("alpha's discard rule must let destroy remove its build output: %v", err)
	}
	if _, err := os.Stat(other.Workspace.Path); err != nil {
		t.Fatal("destroying alpha's lane must not touch beta's")
	}
	if stdout, _, code := run(t, beta.Path, nil, "destroy", "task", "--json"); code == 0 {
		t.Fatalf("beta has no discard rule, so out/ must be preserved, not silently deleted: %s", stdout)
	}
	if _, err := os.Stat(filepath.Join(other.Workspace.Path, "out", "build.dll")); err != nil {
		t.Fatalf("beta's build output must survive: %v", err)
	}
}

func TestProjectSettingsPrecedenceAndRefusals(t *testing.T) {
	t.Parallel()
	r := projectFixture(t, "gamma")
	fromFile := filepath.Join(r.Parent, "from-file")
	fromEnvironment := filepath.Join(r.Parent, "from-environment")
	fromFlag := filepath.Join(r.Parent, "from-flag")
	writeProjectSettings(t, r, "WORKCOPY_ROOT="+fromFile+"\n")

	// flag > environment > project file.
	if lane := laneReport(t, r.Path, nil, 0, "create", "a", "--all").Workspace.Path; filepath.Dir(lane) != fromFile {
		t.Errorf("project file: %s", lane)
	}
	env := []string{"WORKCOPY_ROOT=" + fromEnvironment}
	if lane := laneReport(t, r.Path, env, 0, "create", "b", "--all").Workspace.Path; filepath.Dir(lane) != fromEnvironment {
		t.Errorf("environment must beat the project file: %s", lane)
	}
	if lane := laneReport(t, r.Path, env, 0, "create", "c", "--all", "--root", fromFlag).Workspace.Path; filepath.Dir(lane) != fromFlag {
		t.Errorf("a flag must beat everything: %s", lane)
	}

	// The settings command says where each value came from.
	shown := laneReport(t, r.Path, nil, 0, "settings")
	found := false
	for _, entry := range shown.Settings {
		if entry.Key == "WORKCOPY_ROOT" {
			found = entry.Source == "project" && entry.Origin == filepath.Join(gitDirOf(t, r), "workcopy.env")
		}
	}
	if !found || !strings.Contains(shown.Message, "this repository") {
		t.Errorf("settings must attribute WORKCOPY_ROOT to the project file: %q %+v", shown.Message, shown.Settings)
	}

	// A project file cannot choose a program to run or a directory to link.
	for name, body := range map[string]string{
		"git program": "WORKCOPY_GIT=/bin/true\n",
		"share":       "WORKCOPY_ALLOW_SHARE_TARGET=/tmp\n",
		"relative":    "WORKCOPY_ROOT=lanes\n",
		"typo":        "WORKCOPY_ROOTT=/tmp/x\n",
	} {
		writeProjectSettings(t, r, body)
		stdout, _, code := run(t, r.Path, nil, "list", "--json")
		if code != 2 || errorCode(t, stdout) != "invalid-settings-file" {
			t.Errorf("%s: want exit 2 invalid-settings-file, got %d %s", name, code, stdout)
		}
	}
}

func TestSettingsInitCreatesTheRepositoryFile(t *testing.T) {
	t.Parallel()
	r := projectFixture(t, "delta")
	report := laneReport(t, r.Path, nil, 0, "settings", "--init", "--project-file")
	file := filepath.Join(gitDirOf(t, r), "workcopy.env")
	if !strings.Contains(report.Message, file) {
		t.Fatalf("message must name the file: %q", report.Message)
	}
	body, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(body), "# WORKCOPY_ROOT=") || strings.Contains(string(body), "WORKCOPY_GIT") {
		t.Fatalf("the template must offer the per-repository keys only: %v\n%s", err, body)
	}
	// A second --init must not overwrite what the person edited.
	if _, _, code := run(t, r.Path, nil, "settings", "--init", "--project-file", "--json"); code != 3 {
		t.Fatalf("re-init must refuse (exit 3), got %d", code)
	}
	// The untouched template is valid: every line is a comment.
	laneReport(t, r.Path, nil, 0, "list")
	// It is plain Git-directory content: Git does not see it.
	if status := gitText(t, r.Path, "status", "--porcelain"); strings.Contains(status, "workcopy.env") {
		t.Fatalf("the repository file must be invisible to Git: %q", status)
	}
	// Outside a repository there is nothing to attach it to.
	if stdout, _, code := run(t, t.TempDir(), nil, "settings", "--init", "--project-file", "--json"); code == 0 || errorCode(t, stdout) == "operation-failed" {
		t.Fatalf("--project-file outside a repository must fail with a named error, got %d %s", code, stdout)
	}
}

// Many repositories used at the same moment by separate processes, as when
// several agents each work in their own project.
func TestSeveralRepositoriesAtTheSameTime(t *testing.T) {
	t.Parallel()
	const projects = 4
	repositories := make([]*testutil.Repository, projects)
	for n := range repositories {
		repositories[n] = projectFixture(t, "p"+string(rune('a'+n)))
	}
	var group sync.WaitGroup
	paths := make([]string, projects)
	for n, r := range repositories {
		group.Add(1)
		go func() {
			defer group.Done()
			for _, name := range []string{"task", "review", "spike"} {
				stdout, stderr, code := run(t, r.Path, nil, "create", name, "--all", "--json")
				if code != 0 {
					t.Errorf("%s create %s: %d %s %s", r.Path, name, code, stdout, stderr)
				}
			}
			stdout, _, _ := run(t, r.Path, nil, "path", "task")
			paths[n] = strings.TrimSpace(stdout)
		}()
	}
	group.Wait()
	seen := map[string]bool{}
	for n, r := range repositories {
		listed := laneReport(t, r.Path, nil, 0, "list")
		if len(listed.Workspaces) != 3 {
			t.Errorf("%s: %d lanes, want its own 3", r.Path, len(listed.Workspaces))
		}
		if paths[n] == "" || seen[paths[n]] {
			t.Errorf("%s: lane path %q is empty or shared with another project", r.Path, paths[n])
		}
		seen[paths[n]] = true
		body, err := os.ReadFile(filepath.Join(paths[n], "app", "main.txt"))
		if err != nil || !strings.HasPrefix(string(body), "p"+string(rune('a'+n))) {
			t.Errorf("%s: lane holds another project's content: %v %q", r.Path, err, body)
		}
	}
}

// One WORKCOPY_ROOT shared by every repository (the obvious thing to put in a
// user settings file) must keep their same-named lanes apart or say plainly why
// not; it must never let one repository touch another's lane.
func TestOneSharedRootForSeveralRepositories(t *testing.T) {
	t.Parallel()
	alpha, beta := projectFixture(t, "alpha"), projectFixture(t, "beta")
	shared := filepath.Join(alpha.Parent, "shared-lanes")
	env := []string{"WORKCOPY_ROOT=" + shared}
	first, _, code := run(t, alpha.Path, env, "create", "task", "--all", "--json")
	if code != 0 {
		t.Fatalf("first repository: %d %s", code, first)
	}
	stdout, _, code := run(t, beta.Path, env, "create", "task", "--all", "--json")
	if code == 0 {
		t.Fatalf("a lane named task already exists in the shared root; a second repository must not be handed it: %s", stdout)
	}
	if got := errorCode(t, stdout); got == "operation-failed" {
		t.Fatalf("the refusal must be a named, actionable error, not a raw failure: %s", stdout)
	}
	// Distinct names coexist and each repository sees only its own.
	laneReport(t, beta.Path, env, 0, "create", "task-b", "--all")
	if listed := laneReport(t, alpha.Path, env, 0, "list"); len(listed.Workspaces) != 1 {
		t.Errorf("alpha lists %d lanes in the shared root, want only its own", len(listed.Workspaces))
	}
	if listed := laneReport(t, beta.Path, env, 0, "list"); len(listed.Workspaces) != 1 || listed.Workspaces[0].Name != "task-b" {
		t.Errorf("beta lists %+v, want only task-b", listed.Workspaces)
	}
	if stdout, _, code = run(t, beta.Path, env, "destroy", "task", "--json"); code == 0 {
		t.Fatalf("beta must not be able to destroy alpha's lane: %s", stdout)
	}
}
