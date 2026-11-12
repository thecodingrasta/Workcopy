package mcpserver

import (
	"os"
	"path/filepath"
	"testing"

	"workcopy/internal/testutil"
)

func projectRepository(t *testing.T, content string) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write("A/A.csproj", `<Project/>`)
	r.Write("A/a.txt", content)
	r.Commit(content)
	return r
}

// A repository's own settings file (lane root, owner config) applies to MCP calls
// scoped to it, so the CLI and an agent find the same lanes without a wrapper
// passing repo/root/config, and a neighbouring repository is unaffected.
func TestRepositorySettingsApplyToScopedCalls(t *testing.T) {
	mine, theirs := projectRepository(t, "mine\n"), projectRepository(t, "theirs\n")
	// The fixture parent is canonical (macOS temp directories are symbolic links),
	// and Workcopy reports canonical paths.
	defaultLanes := filepath.Join(mine.Parent, "default-lanes")
	session := connectServer(t, New(nil, "test", Options{Root: defaultLanes}))
	lanes := filepath.Join(mine.Parent, "project-lanes")
	config := filepath.Join(mine.Parent, "owner.json")
	if err := os.WriteFile(config, []byte(`{"schemaVersion":1,"projects":[{"id":"only-in-mine","path":"A"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mine.Path, ".git", "workcopy.env"), []byte("WORKCOPY_ROOT="+lanes+"\nWORKCOPY_CONFIG="+config+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	created := call(t, session, "create", map[string]any{"repo": mine.Path, "name": "task", "projects": []string{"only-in-mine"}, "dryRun": false})
	if created.ExitCode != 0 || created.Report.Workspace.Path != filepath.Join(lanes, "task") {
		t.Fatalf("the repository's lane root and owner config must apply: %+v %+v", created.Error, created.Report.Workspace)
	}
	if created.Scope == nil || created.Scope.Root != lanes {
		t.Fatalf("the returned scope must name the project root: %+v", created.Scope)
	}
	// The neighbour has no file: the server's default placement, and no config.
	other := call(t, session, "create", map[string]any{"repo": theirs.Path, "name": "task", "projects": []string{"only-in-mine"}, "dryRun": true})
	if other.ExitCode == 0 {
		t.Fatalf("a repository without the file must not inherit another's owner config: %+v", other.Report.Plan)
	}
	placed := call(t, session, "create", map[string]any{"repo": theirs.Path, "name": "task", "projects": []string{"A"}, "dryRun": false})
	if placed.ExitCode != 0 || filepath.Dir(placed.Report.Workspace.Path) == lanes || filepath.Dir(filepath.Dir(placed.Report.Workspace.Path)) != defaultLanes {
		t.Fatalf("the neighbour keeps the server's default placement: %+v %+v", placed.Error, placed.Report.Workspace)
	}
	// A call with an explicit root still wins over the file.
	explicit := filepath.Join(mine.Parent, "explicit")
	won := call(t, session, "create", map[string]any{"repo": mine.Path, "root": explicit, "name": "other", "projects": []string{"A"}, "dryRun": false})
	if won.ExitCode != 0 || won.Report.Workspace.Path != filepath.Join(explicit, "other") {
		t.Fatalf("an explicit root beats the repository file: %+v %+v", won.Error, won.Report.Workspace)
	}
}

// A repository file that asks for something it may not is an error the agent can
// read, not a silent fallback.
func TestRepositorySettingsRefusalsReachTheCaller(t *testing.T) {
	session := connectServer(t, New(nil, "test", Options{Root: filepath.Join(t.TempDir(), "lanes")}))
	r := projectRepository(t, "x\n")
	if err := os.WriteFile(filepath.Join(r.Path, ".git", "workcopy.env"), []byte("WORKCOPY_GIT=/bin/true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out := call(t, session, "list", map[string]any{"repo": r.Path})
	if out.ExitCode != 2 || out.Error == nil || out.Error.Code != "invalid-settings-file" {
		t.Fatalf("want invalid-settings-file, got %+v", out.Error)
	}
}
