# Host configuration

Prefer the host's native plugin installer for a compatible package. The portable Agent Plugin uses one executable token, `workcopy`, and a separate `mcp` argument. The host process must be able to resolve that executable on its PATH. Workcopy uses the owner-configured storage location or a repository-specific sibling of the source checkout. Choose a writable location outside the plugin's install and data directories, which the host can remove on uninstall.

For hosts without Agent Plugins support, use the generated target adapter or the host's MCP settings. Merge only the named `workcopy` server entry. Preserve unrelated entries and review any change before saving. Restart/reload the host and verify its MCP tool list.

Do not copy secrets, owner-only settings, or shared dependency paths into a project-level config. Configure the Workcopy root in the user's Workcopy settings when the host adapter does not provide a persistent data directory. `workcopy settings --init` does not overwrite an existing file; edit the created template deliberately and check the effective values with `workcopy settings`.

See `docs/AGENT_PLUGIN_PUBLICATION.md` in the repository for verified per-harness paths and current marketplace status.
