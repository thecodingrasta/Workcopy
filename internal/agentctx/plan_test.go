package agentctx

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"workcopy/internal/agents"
	"workcopy/internal/fsops"
)

func write(t *testing.T, root, name, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

// fixture is a main checkout whose agent directories mix instructions with
// everything that must not be copied.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"AGENTS.md":                       "# rules\n",
		"CLAUDE.md":                       "# claude\n",
		".env":                            "TOKEN=abc\n",
		".kiro/steering/product.md":       "product\n",
		".kiro/steering/tech.md":          "tech\n",
		".kiro/steering/logo.png":         "\x89PNG",
		".kiro/steering/leak.md":          "use ghp_" + strings.Repeat("a1B2c3", 8) + " to push\n",
		".kiro/steering/setup.md":         "api_key = YOUR_API_KEY_GOES_HERE_PLEASE\n",
		".kiro/steering/binary.md":        "a\x00b",
		".kiro/steering/big.md":           strings.Repeat("x", 3000),
		".kiro/specs/feature/design.md":   "design\n",
		".kiro/hooks/lint.kiro.hook":      "{}\n",
		".kiro/sessions/old.md":           "history\n",
		".kiro/settings/mcp.json":         `{"env":{"K":"v"}}`,
		".claude/agents/reviewer.md":      "reviewer\n",
		".claude/commands/ship.md":        "ship\n",
		".claude/settings.local.json":     "{}",
		".claude/projects/p/chat.jsonl":   "{}\n",
		".codex/prompts/p.md":             "prompt\n",
		".codex/config.toml":              "[mcp]\n",
		".cursor/rules/style.mdc":         "style\n",
		".cursorrules":                    "legacy cursor rules\n",
		".gemini/GEMINI.md":               "gemini\n",
		".gemini/settings.json":           "{}",
		".futuretool/rules/a.md":          "new tool rule\n",
		".futuretool/cache/ignored.md":    "cache\n",
		"docs/agents/note.md":             "team note\n",
		"docs/other.md":                   "not context\n",
		".github/copilot-instructions.md": "copilot\n",
	} {
		write(t, root, name, body)
	}
	return root
}

func options() Options {
	return Options{Detect: true, AllAgents: true, MaxFileBytes: 2048, MaxTotalBytes: 1 << 20, MaxFiles: 100}
}

func paths(plan Plan) []string {
	result := []string{}
	for _, f := range plan.Files {
		result = append(result, f.Path)
	}
	sort.Strings(result)
	return result
}

func skippedReason(plan Plan, name string) string {
	for _, s := range plan.Skipped {
		if s.Path == name {
			return s.Reason
		}
	}
	return ""
}

func build(t *testing.T, dir string, opts Options) Plan {
	t.Helper()
	root, err := fsops.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	plan, err := Build(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestDetectionCopiesInstructionsAndNothingElse(t *testing.T) {
	plan := build(t, fixture(t), options())
	want := []string{
		".claude/agents/reviewer.md", ".claude/commands/ship.md", ".codex/prompts/p.md", ".cursor/rules/style.mdc", ".cursorrules",
		".gemini/GEMINI.md", ".github/copilot-instructions.md", ".kiro/hooks/lint.kiro.hook", ".kiro/specs/feature/design.md",
		".kiro/steering/product.md", ".kiro/steering/setup.md", ".kiro/steering/tech.md", "AGENTS.md", "CLAUDE.md",
	}
	if got := paths(plan); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("copied set differs.\n got: %v\nwant: %v\nskipped: %+v", got, want, plan.Skipped)
	}
	for name, why := range map[string]string{
		".kiro/steering/logo.png":       "media",
		".kiro/steering/leak.md":        "credential",
		".kiro/steering/binary.md":      "not text",
		".kiro/steering/big.md":         "larger than",
		".kiro/sessions/old.md":         "",
		".claude/projects/p/chat.jsonl": "",
	} {
		reason := skippedReason(plan, name)
		if why != "" && !strings.Contains(reason, why) {
			t.Errorf("%s: reason %q should mention %q", name, reason, why)
		}
	}
	for _, never := range []string{".env", ".kiro/settings/mcp.json", ".claude/settings.local.json", ".codex/config.toml", ".gemini/settings.json", "docs/other.md", ".futuretool/rules/a.md"} {
		for _, f := range plan.Files {
			if f.Path == never {
				t.Errorf("%s must never be copied", never)
			}
		}
	}
	if strings.Contains(reasonList(plan), "TOKEN") || strings.Contains(reasonList(plan), "ghp_") {
		t.Fatal("a reason must never repeat the secret")
	}
	if len(plan.Tools) < 5 {
		t.Fatalf("tools detected: %v", plan.Tools)
	}
}

func reasonList(plan Plan) string {
	var out strings.Builder
	for _, s := range plan.Skipped {
		out.WriteString(s.Path + " " + s.Reason + "\n")
	}
	return out.String()
}

func TestUnknownToolIsReportedAsACandidateNotCopied(t *testing.T) {
	plan := build(t, fixture(t), options())
	var found *Candidate
	for i := range plan.Candidates {
		if plan.Candidates[i].Path == ".futuretool" {
			found = &plan.Candidates[i]
		}
		if plan.Candidates[i].Path == ".kiro" || plan.Candidates[i].Path == ".github" {
			t.Errorf("%s is known or ignored and must not be a candidate", plan.Candidates[i].Path)
		}
	}
	if found == nil || found.Files != 1 || !strings.Contains(found.Hint, "rules") {
		t.Fatalf("candidate: %+v in %+v", found, plan.Candidates)
	}
	// Adding it by name turns the candidate into a copy and removes the report.
	opts := options()
	opts.Include = []string{".futuretool/rules/**"}
	plan = build(t, fixture(t), opts)
	if !contains(paths(plan), ".futuretool/rules/a.md") {
		t.Fatalf("include list ignored: %v", paths(plan))
	}
	for _, c := range plan.Candidates {
		if c.Path == ".futuretool" {
			t.Fatal("an included directory is no longer a candidate")
		}
	}
	if contains(paths(plan), ".futuretool/cache/ignored.md") {
		t.Fatal("state directories stay denied even inside an included tool")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestOwnerListsLimitsAndTrackedFilesAreHonoured(t *testing.T) {
	dir := fixture(t)
	opts := options()
	opts.Detect = false
	opts.Include = []string{"docs/agents/**", "AGENTS.md"}
	if got := paths(build(t, dir, opts)); strings.Join(got, ",") != "AGENTS.md,docs/agents/note.md" {
		t.Fatalf("with detection off only the include list applies: %v", got)
	}
	opts = options()
	opts.Exclude = []string{".kiro/specs/**", ".claude/**"}
	plan := build(t, dir, opts)
	if contains(paths(plan), ".kiro/specs/feature/design.md") || contains(paths(plan), ".claude/agents/reviewer.md") {
		t.Fatalf("exclude list ignored: %v", paths(plan))
	}
	if skippedReason(plan, ".kiro/specs/feature/design.md") == "" {
		t.Fatal("an excluded file should be reported as excluded")
	}
	opts = options()
	opts.Tracked = func(rel string) bool { return rel == "AGENTS.md" }
	plan = build(t, dir, opts)
	if contains(paths(plan), "AGENTS.md") || skippedReason(plan, "AGENTS.md") != "" {
		t.Fatal("a tracked file is neither copied nor reported: the lane already has it")
	}
	opts = options()
	opts.MaxFiles = 3
	plan = build(t, dir, opts)
	if len(plan.Files) != 3 || !strings.Contains(reasonList(plan), "file-count limit") {
		t.Fatalf("file limit: %d files", len(plan.Files))
	}
	opts = options()
	opts.MaxTotalBytes = 40
	plan = build(t, dir, opts)
	if plan.TotalBytes > 40 || !strings.Contains(reasonList(plan), "total-size limit") {
		t.Fatalf("total limit: %d bytes", plan.TotalBytes)
	}
}

func TestSymbolicLinksAreNeverFollowed(t *testing.T) {
	dir := fixture(t)
	secret := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(secret, []byte("outside the repository\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, ".kiro", "steering", "link.md")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	plan := build(t, dir, options())
	if contains(paths(plan), ".kiro/steering/link.md") || !strings.Contains(skippedReason(plan, ".kiro/steering/link.md"), "symbolic link") {
		t.Fatalf("a symlink must be skipped and reported: %+v", plan.Skipped)
	}
}

func TestExecutableBitIsKeptOnlyWhenAsked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no execute bits on Windows")
	}
	dir := fixture(t)
	script := filepath.Join(dir, ".kiro", "hooks", "run.sh")
	write(t, dir, ".kiro/hooks/run.sh", "#!/bin/sh\n")
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	modeOf := func(opts Options) uint32 {
		for _, f := range build(t, dir, opts).Files {
			if f.Path == ".kiro/hooks/run.sh" {
				return f.Mode
			}
		}
		return 0
	}
	opts := options()
	if modeOf(opts) != 0644 {
		t.Fatal("execute bit must be dropped by default")
	}
	opts.ExecBits = true
	if modeOf(opts) != 0755 {
		t.Fatal("execute bit must be kept when the lane tracks it")
	}
}

func TestDenyReasonAndContentReason(t *testing.T) {
	for name, deny := range map[string]bool{
		".env": true, ".env.local": true, "id_rsa": true, "a/credentials.json": true, "notes/my-secrets.md": true,
		"x/settings.local.json": true, ".kiro/settings/mcp.json": true, "a/logs/x.md": true, "a/node_modules/x.md": true,
		"pic.PNG": true, "bundle.zip": true, "data.sqlite": true, "doc.pdf": true,
		"AGENTS.md": false, ".kiro/steering/a.md": false, "rules/x.mdc": false, ".env.example": false, "a/hooks/run.ps1": false,
	} {
		if got := DenyReason(name) != ""; got != deny {
			t.Errorf("DenyReason(%q) denied=%v, want %v", name, got, deny)
		}
	}
	for body, flagged := range map[string]bool{
		"-----BEGIN RSA PRIVATE KEY-----\nabc": true, "AKIAABCDEFGHIJKLMNOP": true, "token: " + strings.Repeat("Ab1", 10): true,
		"password = hunter2": false, "api_key = YOUR_API_KEY_HERE_REPLACE_ME": false, "Use ${TOKEN_VALUE_FROM_ENV_VARIABLE_HERE}": false,
		"plain prose about tokens and keys": false, "a\x00b": true, "\xff\xfe": true,
	} {
		if got := ContentReason([]byte(body)) != ""; got != flagged {
			t.Errorf("ContentReason(%q) flagged=%v, want %v", body, got, flagged)
		}
	}
}

func TestEveryCatalogueGlobIsWellFormed(t *testing.T) {
	seen := map[string]string{}
	for _, tool := range agents.Catalogue {
		if tool.Name == "" || len(tool.Globs) == 0 {
			t.Errorf("incomplete tool %+v", tool)
		}
		for _, glob := range tool.Globs {
			if strings.HasPrefix(glob, "/") || strings.Contains(glob, "\\") || strings.Contains(glob, "..") || glob == "**" || glob == "*" {
				t.Errorf("%s: unsafe glob %q", tool.Name, glob)
			}
			if other, dup := seen[glob]; dup {
				t.Errorf("%q listed under both %s and %s", glob, other, tool.Name)
			}
			seen[glob] = tool.Name
			// no catalogue glob may reach settings, credentials or sessions
			sample := strings.NewReplacer("**", "x/y", "*", "z").Replace(glob)
			if reason := DenyReason(sample); strings.Contains(reason, "credentials") {
				t.Errorf("%s: %q reaches a credential-looking name", tool.Name, glob)
			}
		}
	}
}
