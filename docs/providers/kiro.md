# Kiro

**Expected format:** Kiro Powers use the Agent Plugins layout with `plugin.json`, optional `mcp.json`, and skills. Kiro supports local folder and GitHub imports. A Power manages its MCP lifecycle. A manual setup can instead install skills and merge an MCP server entry into Kiro settings.

**Workcopy source:** `workcopy_agent_kiro-power` supplies the portable Power package. `workcopy_agent_kiro` supplies `.kiro/skills/` and the manual `mcpServers` example. Both launch the existing `workcopy` executable.

**Remaining check:** The portable Git source includes synchronized notices. Test the managed Power and manual configuration flows in Kiro IDE. Kiro Web/cloud requires Workcopy and the checkout inside its own environment; no Kiro app acceptance is recorded.

**Official references:** [Create a Power](https://kiro.dev/docs/powers/create/), [install Powers](https://kiro.dev/docs/powers/installation/).
