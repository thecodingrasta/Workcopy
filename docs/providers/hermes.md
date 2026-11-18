# Hermes Agent

**Expected format:** Hermes supports the portable Agent Plugins subset: root `plugin.json`, skills, and stdio `mcp.json`. Installing and enabling a plugin are separate operations. Catalog submissions require a reviewed catalog entry pinned to an exact public commit.

**Workcopy source:** The portable package contains the expected manifest, six skills and stdio MCP server. The publication guide documents local install/enable commands and the catalog PR route.

**Remaining check:** The portable Git source includes synchronized notices. Workcopy is not in the Hermes catalog. A catalog submission needs a public source and full commit SHA, followed by maintainer review. No Hermes runtime acceptance is recorded.

**Official references:** [Portable plugin support](https://hermes-agent.nousresearch.com/docs/developer-guide/plugins/), [catalog submission](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/developer-guide/plugins/catalog-submission.md).
