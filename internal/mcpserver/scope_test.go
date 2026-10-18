package mcpserver

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

func TestCommandStartsWithoutRepository(t *testing.T) {
	var stderr strings.Builder
	code := Command(context.Background(), []string{"--root", t.TempDir()}, "test", io.NopCloser(strings.NewReader("")), discardCloser{io.Discard}, &stderr)
	if code != 0 {
		t.Fatalf("multi-repository startup: exit %d: %s", code, stderr.String())
	}
}

func TestRepositoryAndRootScopeArePerCall(t *testing.T) {
	session, first, _ := connect(t, Options{})
	second := testutil.NewRepository(t)
	second.Write("A/A.csproj", `<Project/>`)
	second.Write("A/a.txt", "second\n")
	second.Commit("second repository")
	root := filepath.Join(second.Parent, "custom-lanes")
	args := map[string]any{"repo": second.Path, "root": root, "name": "worker", "projects": []string{"A"}, "dryRun": true}
	preview := call(t, session, "create", args)
	if preview.ExitCode != 0 {
		t.Fatalf("scoped preview: %+v", preview)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("preview persisted root", err)
	}
	args["dryRun"] = false
	created := call(t, session, "create", args)
	if created.ExitCode != 0 || created.Report.Workspace.Path != filepath.Join(root, "worker") {
		t.Fatalf("scoped creation: %+v", created)
	}
	out := call(t, session, "lane_context", map[string]any{"repo": second.Path, "root": root, "name": "worker"})
	if out.ExitCode != 0 || out.Context.Repository != second.Path {
		t.Fatalf("wrong repository context: %+v", out)
	}
	original := call(t, session, "bootstrap", Empty{})
	if original.ExitCode != 0 || original.Context.Repository != first.Path {
		t.Fatal("scope leaked into default repository", original)
	}
	wrong := call(t, session, "destroy", map[string]any{"repo": first.Path, "root": root, "name": "worker", "dryRun": false})
	if wrong.ExitCode != 3 {
		t.Fatalf("cross-repository destroy accepted: %+v", wrong)
	}
	body, err := os.ReadFile(filepath.Join(root, "worker", "A", "a.txt"))
	if err != nil || string(body) != "second\n" {
		t.Fatal("cross-repository source changed", err)
	}
}

func TestRepositoryFreeDefaultsAndRootLocks(t *testing.T) {
	parent, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		parent = resolved
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	s := New(nil, "test", Options{Root: parent, MaxLanes: 1})
	session := connectServer(t, s)
	missing := call(t, session, "bootstrap", Empty{})
	if missing.ExitCode != 2 || missing.Error.Code != "repository-required" {
		t.Fatal("missing repository is not actionable", missing)
	}
	if out := call(t, session, "doctor", Empty{}); out.ExitCode != 0 {
		t.Fatal("repository-free doctor failed", out)
	}
	var roots []string
	for i := 0; i < 2; i++ {
		r := testutil.NewRepository(t)
		r.Write("A/A.csproj", `<Project/>`)
		r.Commit("fixture")
		out := call(t, session, "bootstrap", map[string]any{"repo": r.Path})
		if out.ExitCode != 0 || out.Scope == nil || filepath.Dir(out.Scope.Root) != parent {
			t.Fatalf("default parent not applied: %+v", out)
		}
		roots = append(roots, out.Scope.Root)
		created := call(t, session, "create", map[string]any{"repo": r.Path, "name": "worker", "projects": []string{"A"}, "dryRun": false})
		if created.ExitCode != 0 || created.Report.Workspace.Path != filepath.Join(out.Scope.Root, "worker") {
			t.Fatalf("same-named scoped lane: %+v", created)
		}
		limited := call(t, session, "create", map[string]any{"repo": r.Path, "name": "extra", "projects": []string{"A"}, "dryRun": false})
		if limited.ExitCode != 3 || limited.Error.Code != "lane-limit" {
			t.Fatal("per-root admission bypassed", limited)
		}
	}
	if roots[0] == roots[1] {
		t.Fatal("repository identities collide")
	}
	if !s.beginMutation(roots[0]) {
		t.Fatal("first mutation blocked")
	}
	if s.beginMutation(roots[0]) {
		t.Fatal("same-root writer accepted")
	}
	if !s.beginMutation(roots[1]) {
		t.Fatal("independent-root writer blocked")
	}
	s.endMutation(roots[0])
	s.endMutation(roots[1])
	if len(s.activeRoots) != 0 {
		t.Fatal("completed scopes retained")
	}
}

func TestCleanupAndPreviewRemainAvailableBelowReserve(t *testing.T) {
	session, _, s := connect(t, Options{})
	created := call(t, session, "create", map[string]any{"name": "worker", "projects": []string{"A"}, "dryRun": false})
	if created.ExitCode != 0 {
		t.Fatal(created)
	}
	s.opts.ReserveBytes = ^uint64(0)
	repeated := call(t, session, "expand", map[string]any{"name": "worker", "projects": []string{"A"}, "dryRun": false})
	if repeated.ExitCode != 4 {
		t.Fatal("no-change expansion blocked by allocation reserve", repeated)
	}
	for _, dry := range []bool{true, false} {
		out := call(t, session, "destroy", map[string]any{"name": "worker", "dryRun": dry})
		if out.ExitCode != 0 {
			t.Fatalf("guarded cleanup blocked by allocation reserve: %+v", out)
		}
	}
	preview := call(t, session, "create", map[string]any{"name": "next", "projects": []string{"A"}, "dryRun": true})
	if preview.ExitCode != 0 {
		t.Fatal("read-only preview blocked by reserve", preview)
	}
	blocked := call(t, session, "create", map[string]any{"name": "next", "projects": []string{"A"}, "dryRun": false})
	if blocked.ExitCode != 3 || blocked.Error.Code != "disk-budget" {
		t.Fatal("allocation admitted below reserve", blocked)
	}
}

func TestBootstrapIdentifiesCurrentLaneDirectory(t *testing.T) {
	session, _, _ := connect(t, Options{})
	created := call(t, session, "create", map[string]any{"name": "worker", "projects": []string{"A"}, "dryRun": false})
	if created.ExitCode != 0 {
		t.Fatal(created)
	}
	out := call(t, session, "bootstrap", map[string]any{"directory": filepath.Join(created.Report.Workspace.Path, "A")})
	if out.ExitCode != 0 || out.Context == nil || out.Context.Lane == nil || out.Context.Lane.Name != "worker" || out.Scope.Root != filepath.Dir(created.Report.Workspace.Path) {
		t.Fatalf("current lane was not identified: %+v", out)
	}
}

func TestLinkedCheckoutsHaveSeparateDefaults(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("A/A.csproj", `<Project/>`)
	r.Commit("fixture")
	linked := filepath.Join(r.Parent, "linked", filepath.Base(r.Path))
	r.Git("worktree", "add", "--detach", linked, "HEAD")
	s := New(nil, "test", Options{Root: filepath.Join(r.Parent, "lanes")})
	session := connectServer(t, s)
	first := call(t, session, "bootstrap", map[string]any{"repo": r.Path})
	second := call(t, session, "bootstrap", map[string]any{"repo": linked})
	if first.ExitCode != 0 || second.ExitCode != 0 || first.Scope.Root == second.Scope.Root {
		t.Fatalf("linked checkout storage collision: first=%+v second=%+v", first.Scope, second.Scope)
	}
}

func TestDefaultCheckoutConfigurationAndSiblingRoot(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte(`{"schemaVersion":1,"discovery":{"adapters":["generic"]},"projects":[{"id":"owner","path":"A"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	session, r, s := connect(t, Options{Root: t.TempDir(), ConfigPath: config})
	linked := filepath.Join(r.Parent, "linked")
	r.Git("worktree", "add", "--detach", linked, "HEAD")
	original := call(t, session, "bootstrap", Empty{})
	if original.ExitCode != 0 || original.Report.Catalog.Projects[0].ID != "owner" {
		t.Fatal("default configuration missing", original)
	}
	other := call(t, session, "bootstrap", map[string]any{"repo": linked})
	if other.ExitCode != 0 || other.Report.Catalog.Projects[0].ID == "owner" || filepath.Dir(other.Scope.Root) != filepath.Dir(s.engine.WorkRoot) {
		t.Fatal("override inherited configuration or nested its root", other)
	}
}

func TestResolutionFileRequiresFileMode(t *testing.T) {
	session, _, _ := connect(t, Options{})
	out := call(t, session, "resolve", map[string]any{"name": "worker", "path": "A/a.txt", "token": "unused", "mode": "local", "fromFile": "A/a.txt", "dryRun": true})
	if out.ExitCode != 2 {
		t.Fatalf("invalid resolution arguments must return usage error: %+v", out)
	}
}

func TestDistinctUnicodeCheckoutsDoNotInheritDefaults(t *testing.T) {
	first := testutil.NewRepository(t)
	firstPath := filepath.Join(first.Parent, "İ")
	if err := os.Rename(first.Path, firstPath); err != nil {
		t.Fatal(err)
	}
	first.Path = firstPath
	secondPath := filepath.Join(first.Parent, "i")
	if err := os.Mkdir(secondPath, 0700); err != nil {
		t.Skipf("filesystem does not support distinct fixture names: %v", err)
	}
	second := &testutil.Repository{T: t, Parent: first.Parent, Path: secondPath}
	second.Git("init", "-b", "main")
	second.Git("config", "user.name", "Fixture Author")
	second.Git("config", "user.email", "fixture@example.invalid")
	for _, repo := range []*testutil.Repository{first, second} {
		repo.Write("A/A.csproj", `<Project/>`)
		repo.Commit("fixture")
	}
	root := filepath.Join(first.Parent, "lanes", "default")
	engine, err := workspace.New(context.Background(), first.Path, root)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(first.Parent, "config.json")
	if err := os.WriteFile(config, []byte(`{"schemaVersion":1,"discovery":{"adapters":["generic"]},"projects":[{"id":"owner","path":"A"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	session := connectServer(t, New(engine, "test", Options{Root: root, ConfigPath: config}))
	out := call(t, session, "bootstrap", map[string]any{"repo": second.Path})
	if out.ExitCode != 0 || out.Scope.Root == root || out.Report.Catalog.Projects[0].ID == "owner" {
		t.Fatalf("distinct checkout inherited startup scope: %+v", out)
	}
}
