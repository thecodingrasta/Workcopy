package agents

import (
	"strings"
	"testing"
)

// Every MCP client name below is either read from the client's source (Gemini
// CLI announces "gemini-cli-mcp-client", Roo Code "Roo Code") or is the form its
// documentation gives; matching is by fragment so version and decoration do
// not matter. A client that is not recognised must never select a wrong tool.
func TestClientNamesSelectTheirTool(t *testing.T) {
	for client, want := range map[string]string{
		"claude-code": "claude", "Claude Desktop": "claude", "claude-ai": "claude",
		"codex-mcp-client": "codex", "Codex CLI": "codex", "openai-mcp": "codex", "ChatGPT": "codex",
		"gemini-cli-mcp-client": "gemini", "Gemini CLI": "gemini",
		"cursor-vscode": "cursor", "Cursor": "cursor",
		"Visual Studio Code": "copilot", "copilot-chat": "copilot",
		"Roo Code": "roo", "roo-code-1.2": "roo", "Cline": "cline", "cline-mcp": "cline",
		"opencode": "opencode", "OpenCode 0.4": "opencode",
		"kiro": "kiro", "Kiro CLI": "kiro", "kiro-cli": "kiro", "kirocrew": "kiro",
		"windsurf-client": "windsurf", "Windsurf": "windsurf",
		"antigravity": "antigravity", "Junie": "junie", "JetBrains AI": "junie",
		"goose": "goose", "Zed": "zed", "zed-editor": "zed", "continue-client": "continue",
		"kilo-code": "kilo", "Amazon Q Developer": "amazonq", "trae-ide": "trae",
	} {
		if got := Match(client); got != want {
			t.Errorf("Match(%q) = %q, want %q", client, got, want)
		}
	}
	// Short aliases need a word to start with them, so a coincidence inside a
	// longer word does not pick a tool.
	for _, client := range []string{"", "   ", "my-custom-agent", "optimized-agent", "serialized", "bruce", "mcp-inspector", "test-client"} {
		if got := Match(client); got != "" {
			t.Errorf("Match(%q) = %q, want no tool", client, got)
		}
	}
}

func TestSelectionPrecedenceAndFallbacks(t *testing.T) {
	cases := []struct {
		name                 string
		explicit, configured []string
		client               string
		all                  bool
		keys                 string
		source               string
	}{
		{"detected client", nil, nil, "claude-code", false, "claude", "detected"},
		{"unknown client includes everything", nil, nil, "mystery", true, "", "not recognised"},
		{"no client includes everything", nil, nil, "", true, "", "no client"},
		{"auto setting detects", nil, []string{"auto"}, "gemini-cli-mcp-client", false, "gemini", "detected"},
		{"setting overrides the client", nil, []string{"kiro", "codex"}, "claude-code", false, "codex,kiro", "agents setting"},
		{"explicit overrides the setting", []string{"cursor"}, []string{"kiro"}, "claude-code", false, "cursor", "requested"},
		{"all", []string{"all"}, []string{"kiro"}, "", true, "", "requested"},
		{"auto plus a fixed tool", nil, []string{"auto", "kiro"}, "codex-mcp-client", false, "codex,kiro", "detected"},
		{"auto with unknown client widens", nil, []string{"auto", "kiro"}, "mystery", true, "", "not recognised"},
		{"generic only", nil, []string{"generic"}, "claude-code", false, "", "agents setting"},
		{"case and spaces", []string{" Kiro ", "CODEX"}, nil, "", false, "codex,kiro", "requested"},
	}
	for _, c := range cases {
		got := Resolve(c.explicit, c.configured, c.client)
		if got.All != c.all || strings.Join(got.Keys, ",") != c.keys || !strings.Contains(got.Source, c.source) {
			t.Errorf("%s: got %+v, want all=%v keys=%q source~%q", c.name, got, c.all, c.keys, c.source)
		}
	}
}

func TestValidSelectorCoversEveryCatalogueKey(t *testing.T) {
	for _, tool := range Catalogue {
		if !ValidSelector(tool.Key) {
			t.Errorf("%s key %q is not a valid selector", tool.Name, tool.Key)
		}
		if tool.Key != GenericKey && len(tool.Aliases) == 0 {
			t.Errorf("%s has no client aliases, so MCP detection can never select it", tool.Name)
		}
	}
	for _, bad := range []string{"", "kirо", "everything", "claude code"} {
		if ValidSelector(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if !ValidSelector("ALL") || !ValidSelector(" auto ") {
		t.Error("selectors are case and space insensitive")
	}
}
