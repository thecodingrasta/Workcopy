package acceptance

import (
	"os"
	"path/filepath"
	"testing"

	"workcopy/internal/testutil"
)

// TestMCPDiscoverListExpandSolutionShareUnshareRecover exercises the seven
// workcopy_* MCP tools that TestMCPThreeWorkersValidateAndHandOffReviews and
// TestMCPMultipleRepositoriesAndDirectoryOnboarding do not call: discover,
// list, expand, solution, share, unshare and recover. Each tool gets at
// least one real success-path call through the official SDK client and, for
// the ones with a documented guard, a deliberate rejection-path call.
func TestMCPDiscoverListExpandSolutionShareUnshareRecover(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("projects/a/go.mod", "module fixture/a\n\ngo 1.25.0\n")
	r.Write("projects/a/value.go", "package a\nfunc Value() int { return 1 }\n")
	r.Write("projects/a/value_test.go", "package a\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal(Value()) } }\n")
	r.Write("projects/a/A.csproj", "<Project Sdk=\"Microsoft.NET.Sdk\"></Project>\n")
	r.Write("projects/b/go.mod", "module fixture/b\n\ngo 1.25.0\n")
	r.Write("projects/b/value.go", "package b\nfunc Value() int { return 2 }\n")
	r.Write("projects/b/value_test.go", "package b\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 2 { t.Fatal(Value()) } }\n")
	r.Write("App.sln", "Microsoft Visual Studio Solution File, Format Version 12.00\n"+
		"Project(\"{FAE04EC0-301F-11D3-BF4B-00C04F79EFBC}\") = \"A\", \"projects\\a\\A.csproj\", \"{11111111-1111-1111-1111-111111111111}\"\nEndProject\n")
	r.Commit("fixture with two projects and a solution file")

	root := filepath.Join(r.Parent, "lanes")

	// --- discover ---------------------------------------------------------
	// discover is read-only and independent of any lane; run it against a
	// fresh session before any lane exists.
	discoverSession := startMCP(t, r.Path, root)
	discovered := mcpCall(t, discoverSession, "discover", map[string]any{}, 0)
	if discovered.Report.Catalog == nil || len(discovered.Report.Catalog.Projects) < 2 {
		t.Fatalf("discover did not surface committed projects: %+v", discovered.Report)
	}

	// --- list (empty) -------------------------------------------------------
	empty := mcpCall(t, discoverSession, "list", map[string]any{}, 0)
	if len(empty.Report.Workspaces) != 0 {
		t.Fatalf("list reported lanes before any lane was created: %+v", empty.Report.Workspaces)
	}

	// Create the lane used for the remaining tool calls, selecting only
	// project a so expand has something real to add later.
	mcpCall(t, discoverSession, "create", map[string]any{"name": "coverage", "projects": []string{"projects/a", "App.sln"}, "dryRun": true}, 0)
	created := mcpCall(t, discoverSession, "create", map[string]any{"name": "coverage", "projects": []string{"projects/a", "App.sln"}, "dryRun": false}, 0)
	lane := created.Report.Workspace.Path
	if _, err := os.Stat(filepath.Join(lane, "projects", "b")); !os.IsNotExist(err) {
		t.Fatal("project b should not be present before expand")
	}

	// --- list (one lane) -----------------------------------------------------
	withLane := mcpCall(t, discoverSession, "list", map[string]any{}, 0)
	if len(withLane.Report.Workspaces) != 1 || withLane.Report.Workspaces[0].Name != "coverage" {
		t.Fatalf("list did not reflect the created lane: %+v", withLane.Report.Workspaces)
	}

	// --- expand ----------------------------------------------------------
	mcpCall(t, discoverSession, "expand", map[string]any{"name": "coverage", "projects": []string{"projects/b"}, "dryRun": true}, 0)
	if _, err := os.Stat(filepath.Join(lane, "projects", "b")); !os.IsNotExist(err) {
		t.Fatal("expand dry-run must not persist a selection")
	}
	mcpCall(t, discoverSession, "expand", map[string]any{"name": "coverage", "projects": []string{"projects/b"}, "dryRun": false}, 0)
	if _, err := os.Stat(filepath.Join(lane, "projects", "b", "value.go")); err != nil {
		t.Fatalf("expand did not materialize project b: %v", err)
	}
	// Repeating an identical expansion is documented to return no-change (4).
	mcpCall(t, discoverSession, "expand", map[string]any{"name": "coverage", "projects": []string{"projects/b"}, "dryRun": false}, 4)

	// --- solution ----------------------------------------------------------
	solved := mcpCall(t, discoverSession, "solution", map[string]any{"name": "coverage", "path": "App.sln", "dryRun": false}, 0)
	if solved.Report.Plan == nil || len(solved.Report.Plan.Files) != 1 {
		t.Fatalf("solution did not report a generated filter path: %+v", solved.Report)
	}
	if _, err := os.Stat(filepath.Join(lane, solved.Report.Plan.Files[0])); err != nil {
		t.Fatalf("solution did not materialize the reported .slnf: %v", err)
	}

	// --- share: rejection path first (target not on the owner allowlist) ---
	rejectShare := startMCP(t, r.Path, root) // no --allow-share-target configured
	cache := t.TempDir()
	rejected := mcpCall(t, rejectShare, "share", map[string]any{
		"name": "coverage", "path": "shared-cache", "target": cache,
		"dependencyIdentity": "fixture-lock", "trustShared": true, "dryRun": false,
	}, 3)
	if rejected.Error == nil || rejected.Error.Code != "share-target-not-allowed" {
		t.Fatalf("share without an allowlisted target should be rejected: %+v", rejected.Error)
	}

	// --- share: success path with the target explicitly allowlisted --------
	if err := os.WriteFile(filepath.Join(cache, "marker.txt"), []byte("shared\n"), 0600); err != nil {
		t.Fatal(err)
	}
	allowedSession := startMCPArgs(t, "--repo", r.Path, "--root", root, "--max-lanes", "5", "--allow-share-target", cache)
	mcpCall(t, allowedSession, "share", map[string]any{
		"name": "coverage", "path": "shared-cache", "target": cache,
		"dependencyIdentity": "fixture-lock", "trustShared": true, "dryRun": true,
	}, 0)
	mcpCall(t, allowedSession, "share", map[string]any{
		"name": "coverage", "path": "shared-cache", "target": cache,
		"dependencyIdentity": "fixture-lock", "trustShared": true, "dryRun": false,
	}, 0)
	if body, err := os.ReadFile(filepath.Join(lane, "shared-cache", "marker.txt")); err != nil || string(body) != "shared\n" {
		t.Fatalf("share did not create a lane-local link reaching the target: %v", err)
	}

	// --- unshare: removes the link, retains the target ----------------------
	mcpCall(t, allowedSession, "unshare", map[string]any{"name": "coverage", "path": "shared-cache", "dryRun": false}, 0)
	if _, err := os.Stat(filepath.Join(lane, "shared-cache")); !os.IsNotExist(err) {
		t.Fatal("unshare should remove the lane-local link")
	}
	if body, err := os.ReadFile(filepath.Join(cache, "marker.txt")); err != nil || string(body) != "shared\n" {
		t.Fatalf("unshare must retain the shared target itself: %v", err)
	}

	// --- recover: no-op on a healthy lane -----------------------------------
	mcpCall(t, allowedSession, "recover", map[string]any{"name": "coverage", "dryRun": true}, 0)
	mcpCall(t, allowedSession, "recover", map[string]any{"name": "coverage", "dryRun": false}, 4)

	// Final sanity: original checkout untouched by any of the above tool
	// calls, matching every other MCP acceptance test's invariant.
	if status := r.Git("status", "--porcelain=v1"); status != "" {
		t.Fatalf("original checkout was mutated by MCP tool coverage: %q", status)
	}
}
