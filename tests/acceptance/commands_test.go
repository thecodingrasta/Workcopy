package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

// Commands that no other acceptance test ran through the shipped binary.
func TestVersionLegacyAndSolutionThroughTheBinary(t *testing.T) {
	out, _, code := run(t, t.TempDir(), nil, "version", "--json")
	var version workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &version) != nil || !strings.HasPrefix(version.Message, "Workcopy ") {
		t.Fatalf("version: %d %s", code, out)
	}
	if _, _, code = run(t, t.TempDir(), nil, "version", "extra", "--json"); code != 2 {
		t.Fatalf("version takes no arguments, got exit %d", code)
	}

	// legacy: read-only inventory of an old lane's metadata, and a clear failure otherwise.
	old := t.TempDir()
	if err := os.MkdirAll(filepath.Join(old, ".workcopy"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, ".workcopy", "baseline.json"), []byte(`{"v":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, _, code = run(t, t.TempDir(), nil, "legacy", "--workspace", old, "--json")
	var legacy workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &legacy) != nil || !strings.Contains(legacy.Message, "baseline.json") || !strings.Contains(legacy.Message, "no adoption") {
		t.Fatalf("legacy: %d %s", code, out)
	}
	if body, _ := os.ReadFile(filepath.Join(old, ".workcopy", "baseline.json")); string(body) != `{"v":1}` {
		t.Fatal("legacy inspection must not change anything")
	}
	if out, _, code = run(t, t.TempDir(), nil, "legacy", "--workspace", t.TempDir(), "--json"); code != 3 || errorCode(t, out) != "legacy-metadata-not-found" {
		t.Fatalf("legacy on a directory with no metadata: %d %s", code, out)
	}

	// solution: a .slnf that lists only the projects present in the lane.
	r := testutil.NewFixtureRepository(t, "large-monorepo")
	root := filepath.Join(r.Parent, "lanes")
	if out, _, code = run(t, r.Path, nil, "create", "sln", "--project", "project-01", "--root", root, "--json"); code != 0 {
		t.Fatalf("create: %d %s", code, out)
	}
	out, _, code = run(t, r.Path, nil, "solution", "sln", "--solution", "Monorepo.sln", "--root", root, "--json")
	var solved workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &solved) != nil || solved.Plan == nil || len(solved.Plan.Files) != 1 {
		t.Fatalf("solution: %d %s", code, out)
	}
	filter, err := os.ReadFile(filepath.Join(root, "sln", filepath.FromSlash(solved.Plan.Files[0])))
	if err != nil || !strings.Contains(string(filter), "project-01") || strings.Contains(string(filter), "project-02") {
		t.Fatalf("the filter must list only the selected project: %v\n%s", err, filter)
	}
	if _, _, code = run(t, r.Path, nil, "solution", "sln", "--solution", "Nope.sln", "--root", root, "--json"); code == 0 {
		t.Fatal("a missing solution must fail")
	}
}

// The settings file, the environment and flags, through the shipped binary.
func TestSettingsPrecedenceAndInitThroughTheBinary(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("a.txt", "x\n")
	r.Commit("base")
	fileRoot := filepath.Join(r.Parent, "from-file")
	envRoot := filepath.Join(r.Parent, "from-env")
	flagRoot := filepath.Join(r.Parent, "from-flag")
	file := filepath.Join(r.Parent, "workcopy.env")
	if err := os.WriteFile(file, []byte("# my preferences\nWORKCOPY_ROOT="+fileRoot+"\nWORKCOPY_JSON=on\nWORKCOPY_TOOL_TIMEOUT=15m\n"), 0600); err != nil {
		t.Fatal(err)
	}
	useFile := []string{"WORKCOPY_SETTINGS=" + file}

	// WORKCOPY_JSON=on makes JSON the default, and WORKCOPY_ROOT places the lane.
	out, _, code := run(t, r.Path, useFile, "create", "l1", "--all")
	var created workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &created) != nil || !strings.HasPrefix(created.Workspace.Path, fileRoot) {
		t.Fatalf("file preferences were not applied: %d %s", code, out)
	}
	show := func(environment []string, arguments ...string) map[string]struct{ Source, Origin, Value string } {
		out, _, code := run(t, r.Path, environment, append([]string{"settings", "--json"}, arguments...)...)
		var report workspace.Report
		if code != 0 || json.Unmarshal([]byte(out), &report) != nil {
			t.Fatalf("settings: %d %s", code, out)
		}
		result := map[string]struct{ Source, Origin, Value string }{}
		for _, entry := range report.Settings {
			result[entry.Key] = struct{ Source, Origin, Value string }{entry.Source, entry.Origin, strings.Join(entry.Values, ";")}
		}
		return result
	}
	if got := show(useFile)["WORKCOPY_ROOT"]; got.Source != "file" || got.Value != fileRoot || got.Origin != file {
		t.Fatalf("root from the file: %+v", got)
	}
	if got := show(useFile)["WORKCOPY_TOOL_TIMEOUT"]; got.Value != "15m" || got.Source != "file" {
		t.Fatalf("timeout from the file: %+v", got)
	}
	if got := show(useFile)["WORKCOPY_MAX_LANES"]; got.Source != "default" || got.Value != "32" {
		t.Fatalf("untouched keys keep their defaults: %+v", got)
	}
	if got := show(append([]string{"WORKCOPY_ROOT=" + envRoot}, useFile...))["WORKCOPY_ROOT"]; got.Source != "environment" || got.Value != envRoot {
		t.Fatalf("the environment beats the file: %+v", got)
	}
	if got := show(append([]string{"WORKCOPY_ROOT=" + envRoot}, useFile...), "--root", flagRoot)["WORKCOPY_ROOT"]; got.Source != "flag" || got.Value != flagRoot {
		t.Fatalf("a flag beats the environment: %+v", got)
	}
	// list honours the same precedence when deciding where lanes live.
	if out, _, code = run(t, r.Path, useFile, "list"); code != 0 || !strings.Contains(out, "l1") {
		t.Fatalf("list should find the lane under the file's root: %d %s", code, out)
	}
	if out, _, code = run(t, r.Path, append([]string{"WORKCOPY_ROOT=" + envRoot}, useFile...), "list"); code != 0 || strings.Contains(out, "l1") {
		t.Fatalf("the environment root has no lanes: %d %s", code, out)
	}

	// Mistakes are named, not ignored.
	for name, body := range map[string]string{"typo": "WORKCOPY_TOOL_TIMEOT=5m\n", "range": "WORKCOPY_TOOL_TIMEOUT=1s\n", "syntax": "WORKCOPY_ROOT\n"} {
		bad := filepath.Join(r.Parent, name+".env")
		if err := os.WriteFile(bad, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		out, _, code = run(t, r.Path, []string{"WORKCOPY_SETTINGS=" + bad}, "list", "--json")
		if code != 2 {
			t.Errorf("%s: want exit 2, got %d %s", name, code, out)
		}
	}
	if out, _, code = run(t, r.Path, []string{"WORKCOPY_SETTINGS=" + filepath.Join(r.Parent, "absent.env")}, "list", "--json"); code != 2 || errorCode(t, out) != "settings-file-not-found" {
		t.Fatalf("an explicitly named missing file is an error: %d %s", code, out)
	}

	// --init writes a commented template once and never overwrites.
	fresh := filepath.Join(r.Parent, "init", "workcopy.env")
	if out, _, code = run(t, r.Path, nil, "settings", "--init", "--settings", fresh, "--json"); code != 0 {
		t.Fatalf("init: %d %s", code, out)
	}
	template, err := os.ReadFile(fresh)
	if err != nil || !strings.Contains(string(template), "# WORKCOPY_TOOL_TIMEOUT=") || !strings.Contains(string(template), "Precedence") {
		t.Fatalf("template: %v", err)
	}
	if out, _, code = run(t, r.Path, nil, "settings", "--init", "--settings", fresh, "--json"); code != 3 || errorCode(t, out) != "settings-file-exists" {
		t.Fatalf("init must not overwrite: %d %s", code, out)
	}
	if out, _, code = run(t, r.Path, nil, "settings", "--settings", fresh); code != 0 || !strings.Contains(out, "WORKCOPY_AGENT_CONTEXT_INCLUDE") {
		t.Fatalf("human settings output: %d %s", code, out)
	}
}

// The agent-context journey through the binary: preview, create, edit the
// settings once, refresh, and see the guidance a person or agent needs.
func TestAgentContextThroughTheBinary(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("AGENTS.md", "tracked\n")
	r.Write("src/a.txt", "x\n")
	r.Commit("base")
	for name, body := range map[string]string{
		".kiro/steering/product.md": "product\n",
		".kiro/settings/mcp.json":   `{"env":{"TOKEN":"abc"}}`,
		".codex/prompts/p.md":       "prompt\n",
		".newtool/rules/a.md":       "a rule from a tool nobody listed\n",
		".claude/sessions/x.md":     "history\n",
	} {
		r.Write(name, body)
	}
	file := filepath.Join(r.Parent, "workcopy.env")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	env := []string{"WORKCOPY_SETTINGS=" + file}
	root := filepath.Join(r.Parent, "lanes")

	out, _, code := run(t, r.Path, env, "agent-context", "--json")
	var preview workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &preview) != nil || preview.AgentContext == nil {
		t.Fatalf("preview: %d %s", code, out)
	}
	if len(preview.AgentContext.Included) != 2 || len(preview.AgentContext.Candidates) != 1 || preview.AgentContext.Candidates[0].Path != ".newtool" || !strings.Contains(preview.AgentContext.Guidance, file) {
		t.Fatalf("preview should list two files, one candidate and name the settings file: %s", out)
	}
	human, _, _ := run(t, r.Path, env, "agent-context")
	if !strings.Contains(human, ".kiro/steering/product.md") || !strings.Contains(human, "Candidate: .newtool") || !strings.Contains(human, "WORKCOPY_AGENT_CONTEXT_INCLUDE") {
		t.Fatalf("human output must show files, candidates and how to change them:\n%s", human)
	}

	out, _, code = run(t, r.Path, env, "create", "l1", "--all", "--root", root, "--json")
	var created workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &created) != nil || len(created.AgentContext.Added) != 2 {
		t.Fatalf("create should copy two files: %d %s", code, out)
	}
	lane := created.Workspace.Path
	if _, err := os.Stat(filepath.Join(lane, ".newtool")); err == nil {
		t.Fatal("an unrecognised tool must not be copied")
	}

	// The owner opts the new tool in once, in the settings file.
	if err := os.WriteFile(file, []byte("WORKCOPY_AGENT_CONTEXT_INCLUDE=.newtool/rules/**\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, _, code = run(t, lane, env, "agent-context", "--refresh", "--dry-run", "--json"); code != 0 {
		t.Fatalf("dry-run refresh: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(lane, ".newtool")); err == nil {
		t.Fatal("dry run wrote a file")
	}
	out, _, code = run(t, lane, env, "agent-context", "--refresh", "--json")
	var refreshed workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &refreshed) != nil || strings.Join(refreshed.AgentContext.Added, ",") != ".newtool/rules/a.md" {
		t.Fatalf("refresh: %d %s", code, out)
	}
	if body, err := os.ReadFile(filepath.Join(lane, ".newtool", "rules", "a.md")); err != nil || string(body) != "a rule from a tool nobody listed\n" {
		t.Fatalf("copied file: %v %q", err, body)
	}
	if _, _, code = run(t, lane, env, "agent-context", "--refresh", "--json"); code != 4 {
		t.Fatalf("a second refresh is a no-op (exit 4), got %d", code)
	}
	if out, _, code = run(t, lane, env, "status", "--json"); code != 0 || strings.Contains(out, ".newtool") || strings.Contains(out, ".kiro") {
		t.Fatalf("copies must not appear in status: %d %s", code, out)
	}
	if out, _, code = run(t, r.Path, env, "destroy", "l1", "--root", root, "--json"); code != 0 {
		t.Fatalf("destroy without --force: %d %s", code, out)
	}
	if body, _ := os.ReadFile(filepath.Join(r.Path, ".kiro", "steering", "product.md")); string(body) != "product\n" {
		t.Fatal("the main checkout's file was touched")
	}
}

// Choosing the agent a lane serves and opting configuration in, through the
// binary: the settings file, the --agent flag, the refresh switch, the rejected
// inputs, and what the report marks as sensitive.
func TestAgentSelectionAndConfigOptInThroughTheBinary(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("AGENTS.md", "tracked\n")
	r.Write("src/a.txt", "x\n")
	r.Commit("base")
	for name, body := range map[string]string{
		".kiro/steering/product.md": "product\n",
		".kiro/settings/mcp.json":   `{"mcpServers":{"mine":{"command":"node"}}}`,
		".codex/prompts/p.md":       "codex prompt\n",
		".claude/agents/r.md":       "claude agent\n",
		".mytool/.env":              "MYTOOL_KEY=" + strings.Repeat("Qz7", 10) + "\n",
		".mytool/server.key":        "-----BEGIN PRIVATE KEY-----\nx\n",
	} {
		r.Write(name, body)
	}
	file := filepath.Join(r.Parent, "workcopy.env")
	if err := os.WriteFile(file, []byte("WORKCOPY_AGENTS=kiro\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := []string{"WORKCOPY_SETTINGS=" + file}
	root := filepath.Join(r.Parent, "lanes")
	has := func(lane, name string) bool {
		_, err := os.Stat(filepath.Join(lane, filepath.FromSlash(name)))
		return err == nil
	}

	// The settings file decides when no flag is given; the flag beats it and says so.
	out, _, code := run(t, r.Path, env, "create", "k1", "--all", "--root", root, "--json")
	var kiro workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &kiro) != nil || strings.Join(kiro.AgentContext.Agents, ",") != "kiro" || !has(kiro.Workspace.Path, ".kiro/steering/product.md") || has(kiro.Workspace.Path, ".codex/prompts/p.md") {
		t.Fatalf("a lane follows the settings file: %d %s", code, out)
	}
	out, _, code = run(t, r.Path, env, "create", "c1", "--all", "--root", root, "--agent", "codex,claude", "--json")
	var multi workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &multi) != nil || strings.Join(multi.AgentContext.Agents, ",") != "claude,codex" || !strings.Contains(multi.AgentContext.AgentsSource, "requested") {
		t.Fatalf("--agent beats the file and accepts commas: %d %s", code, out)
	}
	lane := multi.Workspace.Path
	if !has(lane, ".codex/prompts/p.md") || !has(lane, ".claude/agents/r.md") || has(lane, ".kiro/steering/product.md") {
		t.Fatal("the lane holds the wrong tools")
	}
	if shown, _, _ := run(t, r.Path, env, "settings", "--agent", "gemini"); !strings.Contains(shown, "WORKCOPY_AGENTS") || !strings.Contains(shown, "[flag]") {
		t.Fatalf("settings shows the flag as the source:\n%s", shown)
	}

	// A refresh keeps the recorded tools; --agent moves the lane.
	r.Write(".claude/agents/r.md", "claude agent v2\n")
	out, _, code = run(t, lane, env, "agent-context", "--refresh", "--json")
	var kept workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &kept) != nil || strings.Join(kept.AgentContext.Updated, ",") != ".claude/agents/r.md" || strings.Join(kept.AgentContext.Agents, ",") != "claude,codex" {
		t.Fatalf("refresh keeps the lane's tools: %d %s", code, out)
	}
	out, _, code = run(t, lane, env, "agent-context", "--refresh", "--agent", "kiro", "--json")
	var moved workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &moved) != nil || !has(lane, ".kiro/steering/product.md") || has(lane, ".codex/prompts/p.md") || has(lane, ".claude/agents/r.md") {
		t.Fatalf("--agent --refresh moves the lane to kiro: %d %s", code, out)
	}

	// Mistakes are usage errors that say what is valid.
	for _, args := range [][]string{{"create", "bad", "--all", "--root", root, "--agent", "claud", "--json"}, {"agent-context", "--agent", "everyone", "--json"}} {
		if out, _, code = run(t, r.Path, env, args...); code != 2 || !strings.Contains(out, "claude") {
			t.Fatalf("%v: a misspelt tool is exit 2 listing the valid ones: %d %s", args, code, out)
		}
	}

	// Configuration opt-in is read from the owner's file only.
	optIn := "WORKCOPY_AGENTS=kiro\nWORKCOPY_AGENT_CONTEXT_CONFIG=.kiro/settings/mcp.json;.mytool/.env;.mytool/server.key\nWORKCOPY_AGENT_CONTEXT_SECRETS=.mytool/.env;.mytool/server.key\n"
	if err := os.WriteFile(file, []byte(optIn), 0600); err != nil {
		t.Fatal(err)
	}
	out, _, code = run(t, r.Path, env, "create", "s1", "--all", "--root", root, "--json")
	var secret workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &secret) != nil {
		t.Fatalf("create with opt-in: %d %s", code, out)
	}
	lane = secret.Workspace.Path
	if !has(lane, ".kiro/settings/mcp.json") || !has(lane, ".mytool/.env") || has(lane, ".mytool/server.key") {
		t.Fatalf("opted-in files must arrive and key material must not (mcp=%v env=%v key=%v)", has(lane, ".kiro/settings/mcp.json"), has(lane, ".mytool/.env"), has(lane, ".mytool/server.key"))
	}
	human, refused, _ := run(t, lane, env, "agent-context")
	if !strings.Contains(human, ".mytool/.env") || !strings.Contains(human, "may contain credentials") || !strings.Contains(refused, "server.key") || !strings.Contains(refused, "never copied") {
		t.Fatalf("the report marks the sensitive copy and explains the refused key:\nstdout:\n%s\nstderr:\n%s", human, refused)
	}
	if strings.Contains(human+refused, "MYTOOL_KEY") || strings.Contains(human+refused, "Qz7Qz7") || strings.Contains(human+refused, "BEGIN PRIVATE") {
		t.Fatal("a report must never print file content")
	}
	if out, _, code = run(t, lane, env, "status", "--json"); code != 0 || strings.Contains(out, ".mytool") || strings.Contains(out, "mcp.json") {
		t.Fatalf("copies are not source changes: %d %s", code, out)
	}

	// A wildcard in the secrets list is rejected before anything is copied.
	if err := os.WriteFile(file, []byte("WORKCOPY_AGENT_CONTEXT_SECRETS=.mytool/*\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, _, code = run(t, r.Path, env, "agent-context", "--json"); code != 2 || errorCode(t, out) != "invalid-setting" || !strings.Contains(out, "WORKCOPY_AGENT_CONTEXT_SECRETS") {
		t.Fatalf("a wildcard secrets entry is an invalid setting: %d %s", code, out)
	}
	// So is a configuration pattern that begins with a wildcard.
	if err := os.WriteFile(file, []byte("WORKCOPY_AGENT_CONTEXT_CONFIG=**/*.json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, _, code = run(t, r.Path, env, "agent-context", "--json"); code != 2 || errorCode(t, out) != "invalid-setting" {
		t.Fatalf("a sweeping configuration pattern is an invalid setting: %d %s", code, out)
	}
}
