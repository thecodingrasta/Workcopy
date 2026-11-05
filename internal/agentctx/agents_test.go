package agentctx

import (
	"strings"
	"testing"
)

func TestSelectedAgentsServeOnlyTheirFiles(t *testing.T) {
	dir := fixture(t)
	opts := options()
	opts.AllAgents = false
	opts.Agents = []string{"kiro"}
	plan := build(t, dir, opts)
	got := paths(plan)
	for _, want := range []string{"AGENTS.md", ".kiro/steering/product.md", ".kiro/specs/feature/design.md"} {
		if !contains(got, want) {
			t.Errorf("%s missing from a kiro lane: %v", want, got)
		}
	}
	for _, other := range []string{"CLAUDE.md", ".claude/agents/reviewer.md", ".codex/prompts/p.md", ".cursorrules", ".cursor/rules/style.mdc", ".gemini/GEMINI.md", ".github/copilot-instructions.md"} {
		if contains(got, other) {
			t.Errorf("%s belongs to another tool and was copied: %v", other, got)
		}
	}
	for _, tool := range []string{"claude", "codex", "cursor", "gemini", "copilot"} {
		if !contains(plan.NotSelected, tool) {
			t.Errorf("%s files exist but were not reported as not selected: %v", tool, plan.NotSelected)
		}
	}
	for _, c := range plan.Candidates {
		if c.Path == ".claude" || c.Path == ".codex" || c.Path == ".cursor" || c.Path == ".gemini" {
			t.Errorf("%s is a known tool and must not be offered as an unrecognised candidate", c.Path)
		}
	}

	// Two tools together, and generic-only.
	opts.Agents = []string{"claude", "gemini"}
	got = paths(build(t, dir, opts))
	if !contains(got, "CLAUDE.md") || !contains(got, ".gemini/GEMINI.md") || contains(got, ".kiro/steering/product.md") || !contains(got, "AGENTS.md") {
		t.Errorf("claude+gemini lane: %v", got)
	}
	opts.Agents = nil
	if got = paths(build(t, dir, opts)); strings.Join(got, ",") != "AGENTS.md" {
		t.Errorf("generic-only lane: %v", got)
	}

	// An owner include list is explicit and applies whatever tools are selected.
	opts.Include = []string{".cursorrules"}
	if got = paths(build(t, dir, opts)); !contains(got, ".cursorrules") {
		t.Errorf("include list must not depend on the selection: %v", got)
	}
}

func configFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		".kiro/steering/a.md":               "rule\n",
		".kiro/settings/mcp.json":           `{"servers":{"mine":{"command":"node","args":["tool.js"]}}}`,
		".kiro/settings/tools.toml":         "[tools]\nname = \"mine\"\n",
		".kiro/settings/api-creds.json":     "{}",
		".kiro/settings/credentials.json":   `{"user":"me"}`,
		".kiro/settings/leaky.json":         `{"token": "` + strings.Repeat("Ab1", 12) + `"}`,
		".kiro/settings/server.key":         "-----BEGIN PRIVATE KEY-----\nabc\n",
		".kiro/settings/id_rsa":             "private",
		".kiro/settings/logo.png":           "\x89PNG",
		".kiro/settings/blob.json":          "a\x00b",
		".kiro/settings/sessions/chat.json": "{}",
		".ssh/config":                       "Host x\n",
		".aws/credentials":                  "[default]\n",
		".tools/.env":                       "KEY=" + strings.Repeat("Zy9", 10) + "\n",
		".tools/settings.ini":               "[a]\nb=1\n",
		".tools/deep/inner.cfg":             "x=1\n",
	} {
		write(t, root, name, body)
	}
	return root
}

func TestOwnerConfigOptInCopiesSettingsButNotCredentials(t *testing.T) {
	dir := configFixture(t)
	opts := options()
	opts.AllAgents = false

	// Without an opt-in none of the settings are copied.
	plan := build(t, dir, opts)
	for _, f := range plan.Files {
		if strings.Contains(f.Path, "settings") || strings.HasPrefix(f.Path, ".tools") {
			t.Fatalf("%s copied without an owner opt-in", f.Path)
		}
	}

	opts.Config = []string{".kiro/settings/*.json", ".kiro/settings/*.toml", ".kiro/settings/*", ".tools/**"}
	plan = build(t, dir, opts)
	got := paths(plan)
	for _, want := range []string{".kiro/settings/mcp.json", ".kiro/settings/tools.toml", ".tools/settings.ini", ".tools/deep/inner.cfg"} {
		if !contains(got, want) {
			t.Errorf("%s was opted in and not copied: %v\nskipped: %+v", want, got, plan.Skipped)
		}
	}
	for _, f := range plan.Files {
		if f.Sensitive {
			t.Errorf("%s is marked sensitive without the secrets list", f.Path)
		}
		if f.Source != ConfigSource && f.Source != "Kiro" {
			t.Errorf("%s: source %q", f.Path, f.Source)
		}
	}
	if f := find(plan, ".kiro/settings/mcp.json"); f == nil || f.Source != ConfigSource {
		t.Errorf("an opted-in file is attributed to the config list: %+v", f)
	}

	// Refused whatever the owner opted in, each with a reason that never repeats the secret.
	for name, why := range map[string]string{
		".kiro/settings/credentials.json":   "secrets setting",
		".kiro/settings/leaky.json":         "credential",
		".kiro/settings/server.key":         "never copied",
		".kiro/settings/id_rsa":             "never copied",
		".kiro/settings/logo.png":           "media",
		".kiro/settings/blob.json":          "not text",
		".kiro/settings/sessions/chat.json": "", // pruned while walking: a state directory
		".tools/.env":                       "secrets setting",
	} {
		if contains(got, name) {
			t.Errorf("%s must not be copied under the config list", name)
			continue
		}
		if reason := skippedReason(plan, name); why != "" && !strings.Contains(reason, why) {
			t.Errorf("%s: reason %q should mention %q", name, reason, why)
		}
	}
	if strings.Contains(reasonList(plan), "Zy9") || strings.Contains(reasonList(plan), "Ab1Ab1") {
		t.Fatal("a reason repeated secret material")
	}
	for _, never := range []string{".ssh/config", ".aws/credentials"} {
		if contains(got, never) {
			t.Errorf("%s was copied", never)
		}
	}
}

func TestSecretsListNamesExactFilesAndKeepsTheHardLimits(t *testing.T) {
	dir := configFixture(t)
	opts := options()
	opts.AllAgents = false
	opts.ConfigSecrets = []string{
		".kiro/settings/credentials.json", ".kiro/settings/leaky.json", ".tools/.env",
		".kiro/settings/server.key", ".kiro/settings/id_rsa", ".ssh/config", ".aws/credentials",
		".kiro/settings/blob.json", ".kiro/settings/logo.png",
		".kiro/settings/*", // wildcards are not honoured here
	}
	plan := build(t, dir, opts)
	got := paths(plan)
	for _, want := range []string{".kiro/settings/credentials.json", ".kiro/settings/leaky.json", ".tools/.env"} {
		f := find(plan, want)
		if f == nil {
			t.Errorf("%s was named in the secrets list and not copied: skipped %+v", want, plan.Skipped)
			continue
		}
		if !f.Sensitive || f.Source != SecretSource {
			t.Errorf("%s must be marked sensitive and attributed to the secrets list: %+v", want, f)
		}
	}
	// Key material, credential stores, binaries and non-text stay refused.
	for _, never := range []string{".kiro/settings/server.key", ".kiro/settings/id_rsa", ".ssh/config", ".aws/credentials", ".kiro/settings/blob.json", ".kiro/settings/logo.png"} {
		if contains(got, never) {
			t.Errorf("%s must never be copied, even when named as holding secrets", never)
		}
	}
	// A wildcard in the secrets list opens nothing: the other settings files stay out.
	for _, name := range []string{".kiro/settings/mcp.json", ".kiro/settings/api-creds.json", ".kiro/settings/tools.toml"} {
		if contains(got, name) {
			t.Errorf("%s was reached through a wildcard in the secrets list", name)
		}
	}
	// A sensitive file is scanned for nothing but is still bounded by the size limits.
	opts.MaxFileBytes = 10
	plan = build(t, dir, opts)
	if contains(paths(plan), ".kiro/settings/credentials.json") {
		t.Error("the size limit applies to files in the secrets list")
	}
}

func find(plan Plan, name string) *File {
	for i := range plan.Files {
		if plan.Files[i].Path == name {
			return &plan.Files[i]
		}
	}
	return nil
}
