# OpenCode setup

This adapter installs the canonical skills under `.agents/skills/`, which OpenCode discovers. Merge `mcp.servers.workcopy` from `opencode.json` into the applicable `opencode.json`/`opencode.jsonc` file; do not replace other configuration. The `workcopy` executable must be available on the OpenCode server process PATH. Configure a private Workcopy root using `workcopy settings --init` before creating lanes.

The entry sets `codemode: false` so OpenCode exposes the MCP tools directly. OpenCode prefixes each tool with the server name: for example, `workcopy_doctor` appears as `workcopy_workcopy_doctor`. Confirm the names in the running host before use.

OpenCode's plugin runtime uses its own JS/TS plugin system. This adapter is a skills-plus-MCP setup, not an OpenCode native plugin or an Agent Plugins loader.
