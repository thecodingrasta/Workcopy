# Provider implementation guides

**Provider references checked 18 November 2026.** These pages compare official provider contracts and examples with Workcopy's source and package. Use the publication guide's manual workflow to qualify each supported application.

The common [Agent Plugins 1.0 specification](https://agent-plugins.org/specification) uses root `plugin.json`, `skills/`, and `mcp.json` for a portable plugin. Workcopy's `agent-distribution/workcopy/plugin/` supplies those files, the six canonical skills, README and license notices; its MCP server launches the separately installed Workcopy CLI. A host must have that CLI, Git and the intended checkout available in its own execution environment. The [Agent Skills specification](https://agentskills.io/specification) defines the skill directories and frontmatter. Provider pages describe differences from this common route and link official working examples where available.

| Route | Workcopy implementation | Remaining evidence |
| --- | --- | --- |
| Portable plugin | Devin, Kiro Power, Hermes, Copilot and the ChatGPT desktop local marketplace can use the root plugin. The Git source and ZIP include notices. | Real installation and user journeys in each host. |
| Native plugin | Claude Code, Cursor and Antigravity have host-specific manifests and MCP configuration. | Host acceptance; Antigravity also needs its genuine logo. |
| Skills and MCP configuration | OpenCode, Kiro manual mode and VS Code have host-specific paths/config examples. | Validate discovery and tool names in each host. |
| Provider integration | T3 Code delegates to its configured provider. | Validate the selected provider in T3's environment. |

## Provider pages

- [Claude Code](claude-code.md)
- [Cursor](cursor.md)
- [Antigravity](antigravity.md)
- [OpenAI and Codex](openai-codex.md)
- [GitHub Copilot and VS Code](copilot-vscode.md)
- [Devin](devin.md)
- [OpenCode](opencode.md)
- [Kiro](kiro.md)
- [Hermes Agent](hermes.md)
- [T3 Code](t3-code.md)

See the [publication guide](../AGENT_PLUGIN_PUBLICATION.md) for package names, release checks and submission routes. Recheck official links before release because provider contracts change.
