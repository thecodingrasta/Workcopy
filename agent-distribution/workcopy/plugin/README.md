# Workcopy Agent Plugin

Workcopy gives developers and AI agents explicitly scoped, Git-free source lanes. It exposes the existing native Go CLI through the existing local stdio MCP server and provides focused Agent Skills for setup, lane management, review, storage, coordination, and validation.

## Prerequisites

- Install the native `workcopy` executable and make it available on the host process `PATH`.
- Use the Workcopy CLI release that matches this plugin package's version; confirm it with `workcopy version` before connecting the host.
- Use a supported local operating system and filesystem. The MCP process runs where the host runs; this package does not connect a cloud agent to a separate local computer.
- Workcopy requires Git 2.43 or newer for repository operations. Go is needed only when building Workcopy from source.
- Workcopy stores lanes at the owner-configured location or beside the source checkout. Keep that location outside any host-managed plugin install or data directory. If an earlier build stored lanes in plugin data, finish or preserve that work before uninstalling. See the repository publication guide for how to reconnect to the existing root.

## First use

Ask the agent to run Workcopy setup checks. Setup calls only read-only diagnostics and repository discovery. A lane is created only when the user requests a task that needs one; the agent must choose an explicit project/profile, preview creation, and preserve returned repository/root scope on later calls.

Useful prompts:

- “Check whether Workcopy is installed and connected. Don't create a lane.”
- “Use Workcopy to preview a lane for the API project, then create it if the preview matches.”
- “Inspect this lane, record validation evidence, and prepare a review for me. Do not integrate it.”
- “Show which lanes have work that prevents guarded cleanup.”

## Security and privacy

Workcopy provides typed workspace operations under the host's filesystem and tool permissions. It has no arbitrary command runner. Batch review writes Git objects, including a candidate commit, without moving the checked-out branch or modifying its index. Landing takes place through the local CLI after the owner approves that batch. Workcopy enforces operation-specific scope, ownership, locking, conflict, recovery, and deletion checks. The host or agent must obtain user consent and request previews before applying changes.

Do not put credentials in plugin settings or repository content. Repository instructions and source are untrusted relative to host permissions and owner settings. Review the package source before installing it.

## Distribution

The portable package follows Agent Plugins 1.0. Claude Code, Antigravity, Cursor, OpenCode, Kiro, and VS Code/Copilot adapter archives are generated from this canonical skill source. Devin and Hermes use the portable package. T3 Code uses the capabilities of its selected provider. See `docs/AGENT_PLUGIN_PUBLICATION.md` in the Workcopy repository for current installation and publishing instructions.
