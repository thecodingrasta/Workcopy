# Claude Code

**Expected format:** A Claude plugin has `.claude-plugin/plugin.json`, skills under `skills/`, and an MCP server entry in `.mcp.json`. A Git marketplace uses `.claude-plugin/marketplace.json` and points entries to plugin source directories.

**Workcopy implementation:** `agent-distribution/workcopy/adapters/claude-code/` contains the native manifest, `.mcp.json`, setup README, synchronized skills and license notices. The repository marketplace points to this source. The manifest has a concrete version; `agentdist validate` checks its version, license, source files and skills.

**Release condition:** Install and exercise the plugin in Claude Code on each claimed host. The `workcopy` executable must be on the same host's `PATH`. Static validation has passed, but no application-level acceptance is recorded.

**Official references:** [Claude Code plugin marketplaces](https://code.claude.com/docs/en/plugin-marketplaces), [plugin manifest reference](https://github.com/anthropics/claude-code/blob/main/plugins/plugin-dev/skills/plugin-structure/references/manifest-reference.md).
