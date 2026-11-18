# OpenAI and Codex

**Expected format:** The portable format uses root `plugin.json`, skills and optional `mcp.json`. Optional OpenAI-specific presentation and registered MCP app mappings use `extensions.com.openai` in `plugin.json`; registered app mappings use a root `.app.json`. The desktop app accepts a native `.agents/plugins/marketplace.json` or a legacy-compatible `.claude-plugin/marketplace.json`. Public local stdio submission has organization eligibility and packaging requirements.

**Workcopy implementation:** The portable root has a manifest, six skills and stdio `mcp.json`; a separate skills-only package is also built. The local server command runs the separately installed `workcopy` executable.

**Remaining check:** Test the repository's Claude-compatible catalog in the ChatGPT desktop app. That catalog points to the Claude adapter and provides a local marketplace route for desktop Work and Codex. The portable plugin can also be configured through Codex MCP and skills. Public local stdio submission still requires organization allowlisting and a decision on packaging the executable.

For the native ChatGPT desktop route, an owner can create or merge this example at the Workcopy repository's `.agents/plugins/marketplace.json`. The source path is relative to the repository root. Workcopy ships the Claude-compatible catalog, and this native catalog is an example for local testing or private distribution.

```json
{
  "name": "workcopy-tools",
  "interface": {
    "displayName": "Workcopy Tools"
  },
  "plugins": [
    {
      "name": "workcopy",
      "source": {
        "source": "local",
        "path": "./agent-distribution/workcopy/plugin"
      },
      "policy": {
        "installation": "AVAILABLE",
        "authentication": "ON_INSTALL"
      },
      "category": "Developer tools"
    }
  ]
}
```

Restart ChatGPT desktop and confirm Workcopy appears in the Plugins Directory. Use a real MCP registration if an OpenAI app mapping is needed.

**Official references:** [Package your plugin](https://developers.openai.com/plugins/build/plugins), [desktop-only plugins](https://developers.openai.com/plugins/deploy/desktop-only).
