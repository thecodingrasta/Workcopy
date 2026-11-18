# Workcopy for Cursor

This native Cursor plugin provides Workcopy's skills and local MCP tools. Install the matching Workcopy CLI release on the same host as Cursor and make `workcopy` available on that host's `PATH` before starting the plugin.

The MCP adapter runs `workcopy mcp` and does not set or override `--root`. Workcopy uses its normal settings and default storage behavior; inspect the effective preferences with `workcopy settings` and run the read-only `workcopy doctor` check before using lane operations. You can set an explicit root in the host's MCP arguments when your setup requires it. See the [MCP guide](https://github.com/thecodingrasta/Workcopy/blob/main/docs/mcp.md) and [settings guide](https://github.com/thecodingrasta/Workcopy/blob/main/docs/settings.md).

Use setup and read-only checks first. Preview explicitly scoped changes and obtain the owner's consent before applying them. The owner inspects and approves the exact prepared batch before the authorized agent lands it with the local CLI.
