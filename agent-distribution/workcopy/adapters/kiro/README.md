# Kiro setup

This adapter installs canonical skills under `.kiro/skills/`. Merge the `mcpServers.workcopy` entry from `mcp.json` into `.kiro/settings/mcp.json` (workspace) or the Kiro user MCP file. Preserve other entries. For an installable Kiro Power, use the `workcopy_agent_kiro-power` package, which follows Kiro's Agent Plugins format and includes `plugin.json`, `mcp.json`, and the canonical skills.

The MCP process needs the existing `workcopy` executable on the Kiro host's PATH. Set a private Workcopy root through the Workcopy user settings before lane creation. Kiro Web/cloud runs in its own environment and cannot reach a separate local machine.
