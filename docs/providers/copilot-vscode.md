# GitHub Copilot CLI and VS Code

**Expected format:** Copilot CLI supports a Git-hosted catalog at `.github/plugin/marketplace.json` and portable Agent Plugins with root `plugin.json`, skills and `mcp.json`. VS Code also loads Agent Plugins and supports its native `.vscode/mcp.json` configuration.

**Workcopy implementation:** `.github/plugin/marketplace.json` points to the portable root. The portable package contains its standard manifest, skills and stdio MCP config. A separate VS Code/Copilot adapter supplies Copilot-targeted skill and MCP paths.

**Release condition:** The Copilot CLI is not installed on the validation host; the available command resolves to an installation shim. Complete CLI and VS Code app acceptance before claiming runtime compatibility. Catalog parsing and ZIP validation do not establish host installation success.

**Official references:** [Copilot CLI plugin reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-plugin-reference), [Agent Plugins and marketplaces](https://docs.github.com/en/copilot/reference/plugin-and-marketplace-reference), [VS Code Agent Plugins](https://code.visualstudio.com/docs/agent-customization/agent-plugins).
