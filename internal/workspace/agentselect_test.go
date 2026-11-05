package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/agentctx"
	"workcopy/internal/settings"
	"workcopy/internal/testutil"
)

// multiAgentRepository holds the untracked instruction files of several tools,
// so a test can tell which tool a lane was built for.
func multiAgentRepository(t *testing.T) *testutil.Repository {
	t.Helper()
	r := contextRepository(t)
	for name, body := range map[string]string{
		".codex/prompts/p.md":      "codex prompt\n",
		".cursor/rules/style.mdc":  "cursor style\n",
		".gemini/GEMINI.md":        "gemini notes\n",
		".opencode/agent/build.md": "opencode agent\n",
	} {
		r.Write(name, body)
	}
	return r
}

func laneHas(t *testing.T, lane string, names ...string) []string {
	t.Helper()
	var present []string
	for _, name := range names {
		if exists(t, filepath.Join(lane, filepath.FromSlash(name))) {
			present = append(present, name)
		}
	}
	return present
}

// One lane per tool, through the same engine: the files each receives, the
// record it keeps, and how a later refresh and an explicit change behave.
func TestLaneServesTheAgentsChosenAtCreationAndKeepsThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := multiAgentRepository(t)
	e := contextEngine(t, r, nil)
	all := []string{".kiro/steering/product.md", "CLAUDE.md", ".claude/agents/reviewer.md", ".codex/prompts/p.md", ".cursor/rules/style.mdc", ".gemini/GEMINI.md", ".opencode/agent/build.md"}

	created, err := e.Create(ctx, "kiro-lane", CreateOptions{All: true, Agents: []string{"kiro"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	if got := laneHas(t, lane, all...); strings.Join(got, ",") != ".kiro/steering/product.md" {
		t.Fatalf("a kiro lane received %v", got)
	}
	c := created.AgentContext
	if strings.Join(c.Agents, ",") != "kiro" || !strings.Contains(c.AgentsSource, "requested") {
		t.Fatalf("report must say which agents the lane serves and why: %+v", c)
	}
	for _, want := range []string{"claude", "codex", "cursor", "gemini", "opencode"} {
		if !contains(c.NotSelected, want) {
			t.Errorf("%s files exist but were not reported as not served: %v", want, c.NotSelected)
		}
	}
	if status, statusErr := e.Status(ctx, "kiro-lane"); statusErr != nil || len(status.Changes) != 0 {
		t.Fatalf("copies are not source changes: %+v %v", status.Changes, statusErr)
	}

	// The record, not the current settings, decides what a later refresh and a sync serve.
	r.Write(".kiro/steering/product.md", "product v2\n")
	r.Write("CLAUDE.md", "claude v2\n")
	if done, refreshErr := e.AgentContext(ctx, "kiro-lane", true, false); refreshErr != nil || strings.Join(done.AgentContext.Updated, ",") != ".kiro/steering/product.md" || strings.Join(done.AgentContext.Added, ",") != "" {
		t.Fatalf("refresh must keep serving kiro only: %+v %v", done.AgentContext, refreshErr)
	}
	if got := laneHas(t, lane, "CLAUDE.md", ".claude/agents/reviewer.md"); len(got) != 0 {
		t.Fatalf("claude files appeared in a kiro lane: %v", got)
	}

	// An explicit request moves the lane to another tool: its untouched copies of the old tool go,
	// the new tool's arrive, and the record follows.
	switched, err := e.AgentContextFor(ctx, "kiro-lane", true, false, []string{"claude", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if got := laneHas(t, lane, all...); strings.Join(got, ",") != "CLAUDE.md,.claude/agents/reviewer.md,.codex/prompts/p.md" {
		t.Fatalf("after switching to claude+codex the lane holds %v", got)
	}
	if strings.Join(switched.AgentContext.Agents, ",") != "claude,codex" || strings.Join(sorted(switched.AgentContext.Removed), ",") != ".kiro/steering/product.md,.kiro/steering/tech.md" {
		t.Fatalf("switch outcome: %+v", switched.AgentContext)
	}
	r.Write(".codex/prompts/p.md", "codex prompt v2\n")
	if kept, keptErr := e.AgentContext(ctx, "kiro-lane", true, false); keptErr != nil || strings.Join(kept.AgentContext.Agents, ",") != "claude,codex" || strings.Join(kept.AgentContext.Updated, ",") != ".codex/prompts/p.md" {
		t.Fatalf("the switched selection must persist: %+v %v", kept.AgentContext, keptErr)
	}

	// "all" brings everything back.
	if _, err = e.AgentContextFor(ctx, "kiro-lane", true, false, []string{"all"}); err != nil {
		t.Fatal(err)
	}
	if got := laneHas(t, lane, all...); len(got) != len(all) {
		t.Fatalf("all tools: lane holds %v", got)
	}
}

// The announced MCP client chooses the tool when the owner leaves the setting on
// auto; an owner choice or an explicit request overrides it; an unknown client
// is never guessed at.
func TestMCPClientDecidesTheAgentUnlessTheOwnerOrCallerChooses(t *testing.T) {
	t.Parallel()
	r := multiAgentRepository(t)
	cases := []struct {
		name     string
		values   map[string]string
		client   string
		agents   []string
		wantKeys string
		wantAll  bool
		source   string
	}{
		{"codex client", nil, "codex-mcp-client", nil, "codex", false, "detected from the MCP client"},
		{"gemini client", nil, "gemini-cli-mcp-client", nil, "gemini", false, "detected"},
		{"unknown client gets everything", nil, "my-in-house-agent", nil, "", true, "not recognised"},
		{"no client gets everything", nil, "", nil, "", true, "no client"},
		{"owner setting beats the client", map[string]string{settings.Agents: "kiro,opencode"}, "codex-mcp-client", nil, "kiro,opencode", false, "agents setting"},
		{"auto plus an owner extra", map[string]string{settings.Agents: "auto;kiro"}, "claude-code", nil, "claude,kiro", false, "detected"},
		{"caller beats the owner setting", map[string]string{settings.Agents: "kiro"}, "codex-mcp-client", []string{"cursor"}, "cursor", false, "requested"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := WithClient(context.Background(), c.client)
			e := contextEngine(t, r, c.values)
			preview, err := e.AgentContextFor(ctx, "", false, false, c.agents)
			if err != nil {
				t.Fatal(err)
			}
			got := preview.AgentContext
			isAll := len(got.Agents) == 1 && got.Agents[0] == "all"
			if isAll != c.wantAll || (!c.wantAll && strings.Join(got.Agents, ",") != c.wantKeys) || !strings.Contains(got.AgentsSource, c.source) || got.Client != c.client {
				t.Fatalf("preview: agents=%v source=%q client=%q", got.Agents, got.AgentsSource, got.Client)
			}
			// Creating a lane in the same call applies exactly what the preview promised.
			created, err := e.Create(ctx, "lane", CreateOptions{All: true, Agents: c.agents}, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(created.AgentContext.Added) != len(got.Included) {
				t.Fatalf("created %d files, preview promised %d", len(created.AgentContext.Added), len(got.Included))
			}
			if _, err = e.Destroy(context.Background(), "lane", false, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A lane made before the selection existed carries no record and keeps every tool,
// whatever the current client or setting says.
func TestLaneWithoutARecordKeepsEveryTool(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := multiAgentRepository(t)
	e := contextEngine(t, r, nil)
	if _, err := e.Create(ctx, "old", CreateOptions{All: true, Agents: []string{"kiro"}}, false); err != nil {
		t.Fatal(err)
	}
	opened, err := e.openLane("old")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := newGeneration(opened.snapshot)
	snapshot.Manifest.ContextAgents = nil
	if err = opened.store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	opened.close()

	refreshed, err := e.AgentContext(WithClient(ctx, "codex-mcp-client"), "old", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !(len(refreshed.AgentContext.Agents) == 1 && refreshed.AgentContext.Agents[0] == "all") || !strings.Contains(refreshed.AgentContext.AgentsSource, "predates") {
		t.Fatalf("a lane without a record keeps everything: %+v", refreshed.AgentContext)
	}
	if got := laneHas(t, refreshed.Workspace.Path, "CLAUDE.md", ".codex/prompts/p.md", ".gemini/GEMINI.md"); len(got) != 3 {
		t.Fatalf("legacy lane holds %v", got)
	}
}

// Tool configuration reaches a lane only when the owner lists it. It stays out
// of status, reviews and commits, is discarded by destroy, follows the main
// checkout on refresh, and files that may hold credentials are marked as such.
func TestOwnerOptedInConfigurationFollowsTheLaneContractAndMarksSecrets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := contextRepository(t)
	r.Write(".kiro/settings/mcp.json", `{"mcpServers":{"mine":{"command":"node","args":["tool.js"]}}}`)
	r.Write(".tools/settings.ini", "[mine]\nport=1\n")
	r.Write(".tools/.env", "MINE_API_KEY="+strings.Repeat("Qz7", 10)+"\n")
	r.Write(".tools/id_rsa", "private\n")

	// Not opted in: nothing from the settings tree is copied (the base fixture rule).
	plain := contextEngine(t, r, nil)
	if preview, err := plain.AgentContext(ctx, "", false, false); err != nil || len(preview.AgentContext.Included) == 0 {
		t.Fatalf("baseline preview: %v", err)
	} else {
		for _, f := range preview.AgentContext.Included {
			if strings.Contains(f.Path, "settings") || strings.HasPrefix(f.Path, ".tools") {
				t.Fatalf("%s copied without an opt-in", f.Path)
			}
		}
	}

	e := contextEngine(t, r, map[string]string{
		settings.AgentContextConfig:  ".kiro/settings/mcp.json;.tools/settings.ini;.tools/.env;.tools/id_rsa",
		settings.AgentContextSecrets: ".tools/.env;.tools/id_rsa",
	})
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := created.Workspace.Path
	if got := laneHas(t, lane, ".kiro/settings/mcp.json", ".tools/settings.ini", ".tools/.env", ".tools/id_rsa"); strings.Join(got, ",") != ".kiro/settings/mcp.json,.tools/settings.ini,.tools/.env" {
		t.Fatalf("opted-in files in the lane: %v (a private key must never be copied)", got)
	}
	var sensitive []string
	for _, f := range created.AgentContext.Included {
		if f.Sensitive {
			sensitive = append(sensitive, f.Path)
		}
	}
	if strings.Join(sensitive, ",") != ".tools/.env" {
		t.Fatalf("only the file named in the secrets list is marked sensitive: %v", sensitive)
	}
	if reason := skipReason(created.AgentContext.Skipped, ".tools/id_rsa"); !strings.Contains(reason, "never copied") {
		t.Fatalf("a private key is refused with a reason: %q", reason)
	}
	if blob, _ := os.ReadFile(filepath.Join(lane, ".tools", ".env")); !strings.Contains(string(blob), "MINE_API_KEY") {
		t.Fatal("the named file must be copied byte for byte")
	}
	if status, statusErr := e.Status(ctx, "task"); statusErr != nil || len(status.Changes) != 0 {
		t.Fatalf("configuration copies are not source changes: %+v %v", status.Changes, statusErr)
	}

	// The main checkout's configuration changes; the lane follows, an edited copy is left alone.
	r.Write(".kiro/settings/mcp.json", `{"mcpServers":{"mine":{"command":"node","args":["tool-v2.js"]}}}`)
	r.Write(".tools/settings.ini", "[mine]\nport=2\n")
	if err = os.WriteFile(filepath.Join(lane, ".tools", "settings.ini"), []byte("[mine]\nport=9\n"), 0644); err != nil {
		t.Fatal(err)
	}
	refreshed, err := e.AgentContext(ctx, "task", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(refreshed.AgentContext.Updated, ",") != ".kiro/settings/mcp.json" || strings.Join(refreshed.AgentContext.KeptModified, ",") != ".tools/settings.ini" {
		t.Fatalf("refresh outcome: %+v", refreshed.AgentContext)
	}

	// Review and commit hold source only; destroy discards every copy without --force.
	if err = os.WriteFile(filepath.Join(lane, "src", "a.txt"), []byte("one\nTWO\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	review, err := e.Merge(ctx, "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".kiro", ".tools"} {
		if exists(t, filepath.Join(review.Review.Path, name)) {
			t.Errorf("review contains %s", name)
		}
	}
	committed, err := e.Merge(ctx, "task", MergeOptions{Commit: true, Approve: true, ReviewID: review.Review.ID, Message: "edit"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if tree := r.Git("ls-tree", "-r", "--name-only", committed.Review.Commit); strings.Contains(tree, ".tools") || strings.Contains(tree, "mcp.json") {
		t.Fatalf("the commit must hold source only:\n%s", tree)
	}
	// A lane with no source edits is destroyed without --force: the copies are disposable.
	second, err := e.Create(ctx, "second", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !exists(t, filepath.Join(second.Workspace.Path, ".kiro", "settings", "mcp.json")) {
		t.Fatal("a second lane follows the same owner setting")
	}
	if _, err = e.Destroy(ctx, "second", false, false); err != nil {
		t.Fatalf("configuration copies are disposable and must not need --force: %v", err)
	}
	if exists(t, second.Workspace.Path) {
		t.Fatal("the lane directory was not removed")
	}
	if blob, _ := os.ReadFile(filepath.Join(r.Path, ".tools", ".env")); !strings.Contains(string(blob), "MINE_API_KEY") {
		t.Fatal("the main checkout's original must be untouched")
	}
}

func skipReason(skipped []agentctx.Skipped, name string) string {
	for _, s := range skipped {
		if s.Path == name {
			return s.Reason
		}
	}
	return ""
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
