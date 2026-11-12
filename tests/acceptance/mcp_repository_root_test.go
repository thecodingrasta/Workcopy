package acceptance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/testutil"
)

// The MCP server is usually started with one --root default (a parent directory).
// A repository's own settings file (WORKCOPY_ROOT in its Git directory) must still
// choose that repository's lane root for calls that name it. The startup root is
// only the parent for repositories without such a file; before this was fixed it
// silently overrode every repository's file, so each project's lanes landed under
// the shared default.
func TestMCPRepositorySettingsRootBeatsServerRootDefault(t *testing.T) {
	t.Parallel()
	own := testutil.NewRepository(t)
	own.Write("A/source.txt", "own\n")
	own.Commit("own")
	ownRoot := filepath.Join(own.Parent, "own-lanes")
	if err := os.WriteFile(filepath.Join(own.Path, ".git", "workcopy.env"), []byte("WORKCOPY_ROOT="+ownRoot+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plain := testutil.NewRepository(t)
	plain.Write("A/source.txt", "plain\n")
	plain.Commit("plain")

	serverDefault := filepath.Join(own.Parent, "server-default")
	session := startMCPArgs(t, "--root", serverDefault, "--max-lanes", "5")

	created := mcpCall(t, session, "create", map[string]any{"repo": own.Path, "name": "task", "projects": []string{"A"}, "dryRun": false}, 0)
	if got := created.Report.Workspace.Path; filepath.Dir(got) != ownRoot {
		t.Fatalf("the repository's own WORKCOPY_ROOT must choose its lane root: lane %s, want a child of %s", got, ownRoot)
	}
	// A repository with no settings file keeps the server default as its parent.
	placed := mcpCall(t, session, "create", map[string]any{"repo": plain.Path, "name": "task", "projects": []string{"A"}, "dryRun": false}, 0)
	if got := placed.Report.Workspace.Path; !strings.HasPrefix(got, serverDefault+string(os.PathSeparator)) {
		t.Fatalf("a repository without a settings file must use the server default: lane %s, want under %s", got, serverDefault)
	}
	// Each repository lists only its own lanes, even though both lanes are named task.
	listed := mcpCall(t, session, "list", map[string]any{"repo": own.Path}, 0)
	if len(listed.Report.Workspaces) != 1 || listed.Report.Workspaces[0].Path != created.Report.Workspace.Path {
		t.Fatalf("own repository must list exactly its own lane: %+v", listed.Report.Workspaces)
	}
}
