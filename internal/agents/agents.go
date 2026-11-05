// Package agents is the catalogue of agent tools Workcopy knows: the files each
// reads for instructions, the names their MCP clients announce, and how a
// lane's choice of tools is settled. It has no dependencies, so settings
// validation and manifest checks can share it with the planner in agentctx.
package agents

import (
	"sort"
	"strings"
)

// Tool is one agent product or convention and the repository-relative globs
// that hold its instructions. A glob ending in /** matches the directory tree.
type Tool struct {
	// Key is the short lower-case name used in settings and on the command line.
	Key  string
	Name string
	// Aliases are lower-case fragments of the names a tool's MCP client
	// announces itself with. A client matches when its name contains one.
	Aliases []string
	Globs   []string
}

// GenericKey names the tool-neutral instruction files (AGENTS.md and kin).
// They are copied whatever tools are selected, because most agents read them.
const GenericKey = "generic"

// Catalogue lists conventions known when this version was written (November
// 2026). Tools that are not listed are not copied; they are reported as
// candidates so the owner can add them with WORKCOPY_AGENT_CONTEXT_INCLUDE.
// Settings, credentials and session state of these tools are deliberately not
// matched; the owner can opt specific files in with WORKCOPY_AGENT_CONTEXT_CONFIG.
var Catalogue = []Tool{
	{GenericKey, "Generic instructions", nil, []string{"AGENTS.md", "AGENTS.override.md", "AGENT.md", "CONVENTIONS.md"}},
	{"kiro", "Kiro", []string{"kiro"}, []string{".kiro/steering/**", ".kiro/specs/**", ".kiro/agents/**", ".kiro/hooks/**", ".kiro/skills/**", ".kiro/prompts/**", ".kiro/powers/**", ".kiro/scripts/**"}},
	{"claude", "Claude Code", []string{"claude", "anthropic"}, []string{"CLAUDE.md", "CLAUDE.local.md", ".claude/CLAUDE.md", ".claude/agents/**", ".claude/commands/**", ".claude/skills/**", ".claude/rules/**", ".claude/hooks/**", ".claude/output-styles/**"}},
	{"codex", "Codex", []string{"codex", "openai", "chatgpt"}, []string{".codex/AGENTS.md", ".codex/prompts/**", ".codex/skills/**", ".codex/agents/**"}},
	{"gemini", "Gemini CLI", []string{"gemini"}, []string{"GEMINI.md", ".gemini/GEMINI.md", ".gemini/commands/**", ".gemini/agents/**"}},
	{"antigravity", "Antigravity", []string{"antigravity"}, []string{".agent/rules/**", ".agent/workflows/**", ".agent/skills/**"}},
	{"cursor", "Cursor", []string{"cursor"}, []string{".cursorrules", ".cursor/rules/**", ".cursor/commands/**"}},
	{"windsurf", "Windsurf", []string{"windsurf", "codeium"}, []string{".windsurfrules", ".windsurf/rules/**", ".windsurf/workflows/**", ".windsurf/skills/**"}},
	{"cline", "Cline", []string{"cline"}, []string{".clinerules", ".clinerules/**"}},
	{"roo", "Roo Code", []string{"roo"}, []string{".roorules", ".roo/rules/**", ".roo/rules-*/**", ".roo/commands/**"}},
	{"kilo", "Kilo Code", []string{"kilo"}, []string{".kilocode/rules/**", ".kilocode/workflows/**"}},
	{"continue", "Continue", []string{"continue"}, []string{".continue/rules/**", ".continue/prompts/**"}},
	{"copilot", "GitHub Copilot", []string{"copilot", "visual studio code"}, []string{".github/copilot-instructions.md", ".github/instructions/**", ".github/prompts/**", ".github/agents/**", ".github/chatmodes/**"}},
	{"amazonq", "Amazon Q", []string{"amazon"}, []string{".amazonq/rules/**"}},
	{"junie", "JetBrains Junie", []string{"junie", "jetbrains"}, []string{".junie/guidelines.md", ".junie/guidelines/**"}},
	{"augment", "Augment", []string{"augment"}, []string{".augment/rules/**", ".augment-guidelines"}},
	{"trae", "Trae", []string{"trae"}, []string{".trae/rules/**"}},
	{"openhands", "OpenHands", []string{"openhands", "all-hands"}, []string{".openhands/microagents/**"}},
	{"opencode", "OpenCode", []string{"opencode"}, []string{".opencode/agent/**", ".opencode/command/**"}},
	{"crush", "Crush", []string{"crush"}, []string{"CRUSH.md"}},
	{"warp", "Warp", []string{"warp"}, []string{"WARP.md"}},
	{"goose", "Goose", []string{"goose"}, []string{".goosehints"}},
	{"zed", "Zed", []string{"zed"}, []string{".rules"}},
	{"replit", "Replit", []string{"replit"}, []string{"replit.md"}},
}

// ToolNames lists the catalogue for display.
func ToolNames() []string {
	names := make([]string, 0, len(Catalogue))
	for _, tool := range Catalogue {
		names = append(names, tool.Name)
	}
	return names
}

// Keys lists the selectable tool keys, generic excluded (it is always on).
func Keys() []string {
	keys := []string{}
	for _, tool := range Catalogue {
		if tool.Key != GenericKey {
			keys = append(keys, tool.Key)
		}
	}
	sort.Strings(keys)
	return keys
}

// Selectors are the words a selection may use besides tool keys.
const (
	SelectAll  = "all"
	SelectAuto = "auto"
)

// ValidSelector reports whether word is a tool key, "all" or "auto".
func ValidSelector(word string) bool {
	word = strings.ToLower(strings.TrimSpace(word))
	if word == SelectAll || word == SelectAuto || word == GenericKey {
		return true
	}
	for _, tool := range Catalogue {
		if tool.Key == word {
			return true
		}
	}
	return false
}

// Match returns the tool key whose aliases the announced MCP client name
// contains, or "" when the client is not recognised. Matching is a
// case-insensitive substring test, so version suffixes and decorated names
// ("claude-code", "gemini-cli-mcp-client", "Roo Code") need no table entry each.
// The longest alias wins.
func Match(client string) string {
	client = strings.ToLower(strings.TrimSpace(client))
	if client == "" {
		return ""
	}
	tokens := strings.FieldsFunc(client, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	best, bestLength := "", 0
	for _, tool := range Catalogue {
		for _, alias := range tool.Aliases {
			if len(alias) > bestLength && aliasMatches(alias, client, tokens) {
				best, bestLength = tool.Key, len(alias)
			}
		}
	}
	return best
}

// aliasMatches is a substring test for a distinctive alias, and a whole-word
// prefix test for a short one, so "zed" does not match "optimized-agent".
func aliasMatches(alias, client string, tokens []string) bool {
	if len(alias) > 4 {
		return strings.Contains(client, alias)
	}
	for _, token := range tokens {
		if strings.HasPrefix(token, alias) {
			return true
		}
	}
	return false
}

// Resolution is a settled tool selection and the reason for it.
type Resolution struct {
	// Keys are the selected tool keys; empty means every tool in the catalogue.
	Keys []string `json:"keys,omitempty"`
	// All is true when every tool is selected.
	All bool `json:"all"`
	// Source says where the selection came from, for the report.
	Source string `json:"source"`
}

// Resolve settles which tools a lane serves. Precedence: an explicit request
// (a command-line flag or an MCP argument), then the owner's setting; with
// "auto" the announced MCP client decides, and an unrecognised or absent client
// selects everything rather than guess.
func Resolve(explicit, configured []string, client string) Resolution {
	if len(explicit) > 0 {
		if r, ok := fromWords(explicit, client, "requested for this lane"); ok {
			return r
		}
	}
	if len(configured) > 0 {
		if r, ok := fromWords(configured, client, "the agents setting"); ok {
			return r
		}
	}
	return detect(client)
}

func detect(client string) Resolution {
	if key := Match(client); key != "" {
		return Resolution{Keys: []string{key}, Source: "detected from the MCP client " + quote(client)}
	}
	if strings.TrimSpace(client) != "" {
		return Resolution{All: true, Source: "MCP client " + quote(client) + " is not recognised, so every known tool is included"}
	}
	return Resolution{All: true, Source: "no client announced itself, so every known tool is included"}
}

func quote(s string) string { return "\"" + strings.TrimSpace(s) + "\"" }

func fromWords(words []string, client, source string) (Resolution, bool) {
	keys := []string{}
	seen := map[string]bool{}
	auto := false
	for _, word := range words {
		word = strings.ToLower(strings.TrimSpace(word))
		switch {
		case word == "":
		case word == SelectAll:
			return Resolution{All: true, Source: source}, true
		case word == SelectAuto:
			auto = true
		case word == GenericKey:
		case !seen[word]:
			seen[word] = true
			keys = append(keys, word)
		}
	}
	if auto {
		detected := detect(client)
		if !detected.All {
			for _, key := range detected.Keys {
				if !seen[key] {
					keys = append(keys, key)
				}
			}
			sort.Strings(keys)
			return Resolution{Keys: keys, Source: source + ", with " + detected.Source}, true
		}
		return Resolution{All: true, Source: source + ": " + detected.Source}, true
	}
	if len(keys) == 0 {
		// Only "generic" was named: tool-neutral files and nothing else.
		return Resolution{Source: source}, true
	}
	sort.Strings(keys)
	return Resolution{Keys: keys, Source: source}, true
}
