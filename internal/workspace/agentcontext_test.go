package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"workcopy/internal/settings"
	"workcopy/internal/testutil"
)

// settingsWith resolves settings from environment-style values only, so tests
// never read a developer's real preferences.
func settingsWith(t *testing.T, values map[string]string) *settings.Resolved {
	t.Helper()
	resolved, err := settings.Load(settings.Inputs{
		Environ:   func(key string) (string, bool) { value, ok := values[key]; return value, ok },
		ConfigDir: func() (string, error) { return "", errors.New("none") },
		ReadFile:  func(string) ([]byte, error) { return nil, os.ErrNotExist },
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// contextRepository commits ordinary source and a tracked AGENTS.md, then adds
// the untracked agent files an owner has locally: the realistic OLP shape.
func contextRepository(t *testing.T) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write("AGENTS.md", "tracked policy\n")
	r.Write("src/a.txt", "one\ntwo\nthree\n")
	r.Commit("base")
	for name, body := range map[string]string{
		"CLAUDE.md":                   "untracked claude notes\n",
		".kiro/steering/product.md":   "product steering\n",
		".kiro/steering/tech.md":      "tech steering\n",
		".kiro/steering/logo.png":     "\x89PNG\r\n",
		".kiro/settings/mcp.json":     `{"env":{"TOKEN":"abc"}}`,
		".kiro/sessions/old.md":       "history\n",
		".claude/agents/reviewer.md":  "reviewer agent\n",
		".claude/settings.local.json": "{}",
		".futuretool/rules/a.md":      "rule of a tool nobody listed yet\n",
		"notes.txt":                   "not agent context\n",
	} {
		r.Write(name, body)
	}
	return r
}

func contextEngine(t *testing.T, r *testutil.Repository, values map[string]string) *Engine {
	t.Helper()
	e, err := New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	e.Settings = settingsWith(t, values)
	return e
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return err == nil
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func TestNewLaneReceivesOnlyInstructionContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := contextRepository(t)
	e := contextEngine(t, r, nil)
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	want := []string{".claude/agents/reviewer.md", ".kiro/steering/product.md", ".kiro/steering/tech.md", "CLAUDE.md"}
	if created.AgentContext == nil || strings.Join(sorted(created.AgentContext.Added), ",") != strings.Join(want, ",") {
		t.Fatalf("copied set: %+v", created.AgentContext)
	}
	for _, name := range want {
		if !exists(t, filepath.Join(lane, filepath.FromSlash(name))) {
			t.Errorf("%s was not copied", name)
		}
	}
	for _, name := range []string{".kiro/steering/logo.png", ".kiro/settings/mcp.json", ".kiro/sessions/old.md", ".claude/settings.local.json", ".futuretool/rules/a.md", "notes.txt"} {
		if exists(t, filepath.Join(lane, filepath.FromSlash(name))) {
			t.Errorf("%s must not be copied", name)
		}
	}
	if body, _ := os.ReadFile(filepath.Join(lane, "AGENTS.md")); string(body) != "tracked policy\n" {
		t.Fatal("a tracked file comes from Git, not from the working tree")
	}
	if len(created.AgentContext.Candidates) != 1 || created.AgentContext.Candidates[0].Path != ".futuretool" {
		t.Fatalf("the unrecognised tool should be offered as a candidate: %+v", created.AgentContext.Candidates)
	}
	status, err := e.Status(ctx, "task")
	if err != nil || len(status.Changes) != 0 {
		t.Fatalf("copies must not appear as changes: %+v %v", status.Changes, err)
	}
	if !strings.Contains(created.Message, "Copied 4 agent instruction files") {
		t.Fatalf("message: %s", created.Message)
	}
}

func TestAgentContextNeverReachesAReviewOrACommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := contextRepository(t)
	e := contextEngine(t, r, nil)
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	if err = os.WriteFile(filepath.Join(lane, "src", "a.txt"), []byte("one\nTWO\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// An agent updating its own notes in the lane is not a source change either.
	if err = os.WriteFile(filepath.Join(lane, ".kiro", "steering", "product.md"), []byte("agent scribbles\n"), 0644); err != nil {
		t.Fatal(err)
	}
	review, err := e.Merge(ctx, "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".kiro", ".claude", "CLAUDE.md"} {
		if exists(t, filepath.Join(review.Review.Path, name)) {
			t.Errorf("review contains agent context %s", name)
		}
	}
	committed, err := e.Merge(ctx, "task", MergeOptions{Commit: true, Approve: true, ReviewID: review.Review.ID, Message: "edit"}, false)
	if err != nil {
		t.Fatal(err)
	}
	tree := r.Git("ls-tree", "-r", "--name-only", committed.Review.Commit)
	if strings.Contains(tree, ".kiro") || strings.Contains(tree, "CLAUDE.md") || strings.Contains(tree, ".claude") {
		t.Fatalf("the commit must hold source only:\n%s", tree)
	}
}

func TestDestroyDiscardsCopiesWithoutForceAndLeavesTheMainCheckoutAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := contextRepository(t)
	e := contextEngine(t, r, nil)
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	if err = os.WriteFile(filepath.Join(lane, "CLAUDE.md"), []byte("edited in lane\n"), 0644); err != nil {
		t.Fatal(err)
	}
	planned, err := e.Destroy(ctx, "task", false, true)
	if err != nil || !strings.Contains(strings.Join(planned.Plan.Effects, "\n"), "4 private agent-context copies") || !strings.Contains(strings.Join(planned.Plan.Effects, "\n"), "1 were edited") {
		t.Fatalf("the preview must say what is discarded: %+v %v", planned.Plan, err)
	}
	// An untracked file that is not a copy still blocks deletion.
	if err = os.WriteFile(filepath.Join(lane, "scratch.txt"), []byte("mine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Destroy(ctx, "task", false, false); failureCode(t, err) != "unexported-local-file" {
		t.Fatalf("local work must still be protected: %v", err)
	}
	if err = os.Remove(filepath.Join(lane, "scratch.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Destroy(ctx, "task", false, false); err != nil {
		t.Fatalf("copies are disposable and must not need --force: %v", err)
	}
	if body, _ := os.ReadFile(filepath.Join(r.Path, "CLAUDE.md")); string(body) != "untracked claude notes\n" {
		t.Fatal("the main checkout's original must be untouched")
	}
}

func TestRefreshFollowsTheMainCheckoutWithoutLosingLaneEdits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := contextRepository(t)
	e := contextEngine(t, r, nil)
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	laneFile := func(name string) string {
		body, _ := os.ReadFile(filepath.Join(lane, filepath.FromSlash(name)))
		return string(body)
	}
	// Main changes: update, add, delete. Lane changes: edit one copy, delete one.
	r.Write(".kiro/steering/product.md", "product steering v2\n")
	r.Write(".kiro/steering/new.md", "brand new steering\n")
	if err = os.Remove(filepath.Join(r.Path, ".kiro", "steering", "tech.md")); err != nil {
		t.Fatal(err)
	}
	r.Write("CLAUDE.md", "claude v2\n")
	if err = os.WriteFile(filepath.Join(lane, "CLAUDE.md"), []byte("edited in lane\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(lane, ".claude", "agents", "reviewer.md")); err != nil {
		t.Fatal(err)
	}

	planned, err := e.AgentContext(ctx, "task", true, true)
	if err != nil || strings.Join(planned.AgentContext.Updated, ",") != ".kiro/steering/product.md" || laneFile(".kiro/steering/product.md") != "product steering\n" {
		t.Fatalf("dry run must predict without writing: %+v %v", planned.AgentContext, err)
	}
	done, err := e.AgentContext(ctx, "task", true, false)
	if err != nil {
		t.Fatal(err)
	}
	c := done.AgentContext
	if strings.Join(c.Added, ",") != ".kiro/steering/new.md" || strings.Join(c.Updated, ",") != ".kiro/steering/product.md" || strings.Join(c.Removed, ",") != ".kiro/steering/tech.md" ||
		strings.Join(c.KeptModified, ",") != "CLAUDE.md" || strings.Join(c.KeptMissing, ",") != ".claude/agents/reviewer.md" {
		t.Fatalf("refresh outcome: %+v", c)
	}
	if laneFile(".kiro/steering/product.md") != "product steering v2\n" || laneFile(".kiro/steering/new.md") != "brand new steering\n" ||
		exists(t, filepath.Join(lane, ".kiro", "steering", "tech.md")) || laneFile("CLAUDE.md") != "edited in lane\n" ||
		exists(t, filepath.Join(lane, ".claude", "agents", "reviewer.md")) {
		t.Fatal("lane files do not match the refresh outcome")
	}
	if again, againErr := e.AgentContext(ctx, "task", true, false); againErr != nil || again.ExitCode != 4 {
		t.Fatalf("a second refresh has nothing to do: %+v %v", again.Message, againErr)
	}
	if status, statusErr := e.Status(ctx, "task"); statusErr != nil || len(status.Changes) != 0 {
		t.Fatalf("refresh must not create source changes: %+v %v", status.Changes, statusErr)
	}
}

func TestSyncRefreshesContextUnlessTurnedOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, refresh := range []bool{true, false} {
		r := contextRepository(t)
		values := map[string]string{}
		if !refresh {
			values[settings.AgentContextOnSync] = "off"
		}
		e := contextEngine(t, r, values)
		created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
		if err != nil {
			t.Fatal(err)
		}
		r.Write(".kiro/steering/product.md", "changed after the lane was made\n")
		// First sync with nothing new upstream, then with an upstream commit.
		for step := 0; step < 2; step++ {
			if step == 1 {
				r.Write("src/b.txt", "new upstream file\n")
				r.Git("add", "src/b.txt")
				r.Git("commit", "-m", "upstream")
			}
			if _, err = e.Sync(ctx, "task", false); err != nil {
				t.Fatal(err)
			}
			body, _ := os.ReadFile(filepath.Join(created.Workspace.Path, ".kiro", "steering", "product.md"))
			refreshed := string(body) == "changed after the lane was made\n"
			if refreshed != refresh {
				t.Fatalf("refreshOnSync=%v step %d: lane has %q", refresh, step, body)
			}
			if !refresh {
				break
			}
		}
	}
}

func TestSettingsDecideWhatIsCopied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := contextRepository(t)

	e := contextEngine(t, r, map[string]string{settings.AgentContextInclude: ".futuretool/rules/**", settings.AgentContextExclude: ".kiro/steering/tech.md;.claude/**"})
	preview, err := e.AgentContext(ctx, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, file := range preview.AgentContext.Included {
		got = append(got, file.Path)
	}
	if strings.Join(sorted(got), ",") != ".futuretool/rules/a.md,.kiro/steering/product.md,CLAUDE.md" {
		t.Fatalf("include/exclude lists ignored: %v", got)
	}
	if len(preview.AgentContext.Candidates) != 0 || !strings.Contains(preview.AgentContext.Guidance, settings.AgentContextInclude) {
		t.Fatalf("an included tool is no longer a candidate, and guidance names the setting: %+v", preview.AgentContext)
	}

	e = contextEngine(t, r, map[string]string{settings.AgentContext: "off"})
	created, err := e.Create(ctx, "off", CreateOptions{All: true}, false)
	if err != nil || created.AgentContext != nil || exists(t, filepath.Join(created.Workspace.Path, "CLAUDE.md")) {
		t.Fatalf("switched off means nothing is copied: %+v %v", created.AgentContext, err)
	}

	e = contextEngine(t, r, map[string]string{settings.AgentContextDetect: "off", settings.AgentContextInclude: "CLAUDE.md"})
	created, err = e.Create(ctx, "manual", CreateOptions{All: true}, false)
	if err != nil || strings.Join(created.AgentContext.Added, ",") != "CLAUDE.md" {
		t.Fatalf("with detection off only the include list applies: %+v %v", created.AgentContext, err)
	}

	e = contextEngine(t, r, map[string]string{settings.AgentContextMaxFiles: "2"})
	created, err = e.Create(ctx, "capped", CreateOptions{All: true}, false)
	if err != nil || len(created.AgentContext.Added) != 2 || !strings.Contains(skippedText(created.AgentContext), "file-count limit") {
		t.Fatalf("the file-count limit must apply and be reported: %+v %v", created.AgentContext, err)
	}
}

func skippedText(report *ContextReport) string {
	var out strings.Builder
	for _, s := range report.Skipped {
		out.WriteString(s.Path + ": " + s.Reason + "\n")
	}
	return out.String()
}

func TestRefreshNeverReplacesAFileItDidNotPlace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := contextRepository(t)
	e := contextEngine(t, r, map[string]string{settings.AgentContext: "off"})
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	if err = os.WriteFile(filepath.Join(lane, "CLAUDE.md"), []byte("the agent's own file\n"), 0644); err != nil {
		t.Fatal(err)
	}
	e.Settings = settingsWith(t, nil)
	done, err := e.AgentContext(ctx, "task", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(done.AgentContext.Obstructed, ",") != "CLAUDE.md" {
		t.Fatalf("a different local file must block, not be overwritten: %+v", done.AgentContext)
	}
	if body, _ := os.ReadFile(filepath.Join(lane, "CLAUDE.md")); string(body) != "the agent's own file\n" {
		t.Fatal("the local file was overwritten")
	}
	status, err := e.Status(ctx, "task")
	if err != nil || len(status.Changes) != 1 || status.Changes[0].Path != "CLAUDE.md" {
		t.Fatalf("an unowned local file is an ordinary change: %+v %v", status.Changes, err)
	}
}
