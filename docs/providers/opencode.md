# OpenCode

**Expected format:** OpenCode discovers skills in `.agents/skills/`. Its current MCP configuration uses `mcp.servers.<server>`, `type: "local"`, and a command argument array. OpenCode prefixes each exposed MCP tool with the server name.

**Workcopy source:** The generated adapter supplies `.agents/skills/` and an `opencode.json` example with `mcp.servers.workcopy`. It sets `codemode: false` to expose the tools directly; OpenCode otherwise defaults to Code Mode. This is a skills and MCP adapter, not a JavaScript/TypeScript plugin.

**Remaining check:** Workcopy registers tool names such as `workcopy_doctor`; OpenCode may expose them as `workcopy_workcopy_doctor` because the server name is also `workcopy`. Each canonical skill now explains host prefixes and directs the agent to match the tool suffix. Validate actual discovery and invocation with the supported OpenCode version; no application acceptance is recorded.

**Official references:** [OpenCode skills](https://opencode.ai/docs/skills), [MCP servers and tool naming](https://opencode.ai/docs/mcp-servers), [V1 to V2 config migration](https://opencode.ai/docs/migrate-v1).
