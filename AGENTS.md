# Repository instructions for coding agents

This file governs agents contributing to Workcopy's own source. An agent using the built Workcopy CLI or MCP server as a tool on a different repository wants [docs/agents.md](docs/agents.md) and [docs/mcp.md](docs/mcp.md) instead.

Read `README.md`, `docs/architecture.md` and `CONTRIBUTING.md` before changing behaviour. Use the existing package boundaries. Do not create a second workspace engine or replace the CLI framework without a demonstrated need.

Reproduce defects with real disposable Git repositories. Add a failing regression before a fix. Run `go run ./tools/dev validate` and report exact commands, results and unavailable platforms. Cross-compilation is not native validation.

Never exercise destructive tests against a user's real lanes. Do not weaken ownership checks, skip recovery preconditions, remove journals, hide unresolved conflicts or mark resolved local bytes as upstream baseline. Preserve main HEAD, symbolic HEAD, index and human edits through Workcopy operations.

No automated push, public release, credentials, dependency install, global Git setting or machine configuration change is implied by a coding task. Release publication requires maintainer approval. Keep internal notes and handover material out of the public tree.

**Mandatory version-bump rule:** Prior to any release/versioned commit—or when generating a commit message for a specific version `vX.Y.Z`—you MUST update `"defaultVersion"` in `agent-distribution/workcopy/agentdist.json` and `"version"` in `agent-distribution/workcopy/plugin/plugin.json`, `agent-distribution/workcopy/adapters/claude-code/.claude-plugin/plugin.json`, and `agent-distribution/workcopy/adapters/cursor/.cursor-plugin/plugin.json` to `X.Y.Z`, and verify with `go run ./tools/agentdist validate --release-version X.Y.Z`.
