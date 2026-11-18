---
name: workcopy-setup
description: Check Workcopy installation, version, prerequisites, workspace scope, and MCP connectivity, then guide safe setup. Use when Workcopy is missing, misconfigured, or being connected to an agent harness.
license: GPL-3.0-or-later
---

# Workcopy Setup

MCP hosts may prefix tool names with the server name. Match the Workcopy tool by its full suffix; for example, OpenCode adds the server name to `workcopy_bootstrap`.

Verify the existing Workcopy installation before using it. Setup is read-only: do not create, change, or remove lanes.

## When MCP tools are available

1. Call `workcopy_doctor` to check the runtime, Git executable/version, platform, path warnings, and available disk headroom.
2. Call `workcopy_bootstrap` with the user's current absolute directory (or an explicit source checkout) to identify whether it is a Git checkout or an existing lane and inspect repository/storage context.
3. If a checkout was found, call `workcopy_discover` to inspect committed projects, profiles, and dependency warnings. Do not treat suggested validation candidates as verified build commands.
4. Check that the host has the expected Workcopy MCP tools: bootstrap, discovery, lane context/status, previewed mutations, conflict content/resolution, review, recovery, and guarded cleanup. Use only tools actually present.
5. Report the executable version/platform, selected repository/root scope, missing prerequisites, and the exact next setup action. Never create a lane as a connectivity check.

## When MCP is not connected

Use only safe, read-only checks if the host offers a shell:

1. Locate `workcopy` with the platform's normal executable lookup (`Get-Command workcopy`, `where.exe workcopy`, or `command -v workcopy`). Do not scan unrelated drives or home directories.
2. Run `workcopy version`, `workcopy doctor`, and `workcopy help`. Workcopy requires Git 2.43 or newer for repository operations.
3. If Workcopy is absent or too old, explain the matching release asset and checksum procedure in [installation](references/installation.md). Do not download or execute a binary unless the user requested installation.
4. Explain that stdio MCP runs on the same machine/filesystem as the harness process. A cloud harness sees only its own mounted environment; this plugin does not bridge to the user's computer.
5. For host configuration, use [the harness setup guide](references/host-configuration.md). Show a targeted merge and preserve unrelated settings. Do not overwrite an existing config file.

## Workspace and path checks

- Choose a private local workspace root outside every Git checkout, network share, or cloud-synced directory. On WSL, use a native Linux Workcopy process with Linux paths and Linux storage, or use the Windows executable with Windows paths. Never mix the two path syntaxes.
- Check the current checkout is local and has a commit. Workcopy does not fetch.
- Do not grant the harness broader filesystem access than the user intends. Workcopy project selection is not an OS write allowlist or sandbox.
- Never include credentials in MCP configuration or repository files. Owner settings stay outside repository control.

## Recovery

If executable lookup succeeds but MCP tools are absent, report the exact host, command, and diagnostic result. Check for a path/architecture mismatch, Git availability, and whether the host was restarted after configuration. Do not retry by changing user config, starting another server, or creating a lane without authorization.

See [MCP tool selection](references/tool-contracts.md) when the server is connected.
