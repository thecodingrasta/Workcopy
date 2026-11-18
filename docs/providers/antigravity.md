# Google Antigravity

**Expected format:** A multi-provider plugin may use `.antigravity-plugin/plugin.json`, with `skills/`, `mcp_config.json` and `README.md` at the plugin root. Marketplace submission requires a complete manifest and bundled logo; strict `agy plugin validate --strict` is part of the documented checks.

**Workcopy implementation:** The source adapter is a template containing the multi-provider manifest and `mcp_config.json`. The packager adds the README, notices and six canonical skills. Unpack and validate the generated `workcopy_agent_antigravity` ZIP for local use. With no explicit override, the MCP server uses Workcopy's repository-specific lane storage.

**Gap:** Marketplace submission requires a genuine bundled square logo and a manifest `logo` field. A local validator can pass with a logo note. Ensure the executable is on the host's `PATH`, run strict validation against the release ZIP contents, and complete the app workflow before claiming runtime support.

**Official reference:** [Antigravity build plugins](https://antigravity.google/docs/build-plugins).
