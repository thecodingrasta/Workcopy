# Devin

**Expected format:** Devin CLI accepts portable Agent Plugins with root `plugin.json`, optional `mcp.json`, and skills. It supports local folders, Git sources and repository subdirectories. Team marketplace entries are administrator managed. Cloud sessions execute in Devin's own environment.

**Workcopy source:** Use `agent-distribution/workcopy/plugin/` or the versioned portable ZIP. It contains the portable manifest, stdio MCP config and six skills. The publication guide covers the CLI commands and team distribution route.

**Remaining check:** The portable Git source now includes synchronized `LICENSE` and `COPYRIGHT`. Install Workcopy, Git and the checkout in Devin's execution environment before testing cloud sessions. No Devin application acceptance is recorded.

**Official references:** [Devin CLI plugins](https://docs.devin.ai/cli/extensibility/plugins/overview), [team marketplace](https://docs.devin.ai/cli/extensibility/plugins/quickstart).
