# Cursor

**Expected format:** Cursor supports portable Agent Plugins and native Cursor plugins. Native plugins use `.cursor-plugin/plugin.json`; a repository marketplace uses `.cursor-plugin/marketplace.json`. Cursor's native MCP configuration does not expand portable `${PLUGIN_ROOT}` or `${PLUGIN_DATA}` placeholders; it provides `${CURSOR_PLUGIN_ROOT}` for its own plugin root.

**Workcopy implementation:** Workcopy provides a portable root plugin and a native Cursor adapter with `.cursor-plugin/plugin.json`, `mcp.json`, setup README, synchronized skills and license notices. The marketplace source points at the adapter directory. Its MCP command runs `workcopy mcp` without unsupported portable placeholders.

**Release condition:** Test import, reload, MCP discovery and read-only tool use in Cursor. Confirm local plugin imports are enabled by organization policy where applicable. No app acceptance is recorded.

**Official references:** [Cursor plugins](https://cursor.com/docs/plugins), [Cursor plugin reference](https://prod.cursor.com/docs/reference/plugins).
