---
name: workcopy-lane-manager
description: Discover repository projects and create, inspect, or expand explicitly scoped Workcopy lanes for independent tasks. Use when a task needs an isolated Git-free source copy or coordinated writers.
license: GPL-3.0-or-later
---

# Workcopy Lane Manager

MCP hosts may prefix tool names with the server name. Match the Workcopy tool by its full suffix; for example, OpenCode adds the server name to `workcopy_bootstrap`.

Use Workcopy's existing MCP tools first. Workcopy creates plain folders from committed source; it does not run agents, builds, restores, or arbitrary shell commands. A lane is not a security sandbox.

## Discover and select

1. Call `workcopy_bootstrap` for the current absolute checkout path. Read the repository identity, available root, headroom, warnings, and any existing lane identity.
2. Save returned `scope.repo` and `scope.root`; pass them on each later tool call and worker handoff.
3. Call `workcopy_discover`. Inspect project IDs/paths, profiles, dependency warnings, and pages. Request subsequent pages when `nextOffset` is nonzero.
4. Select the smallest set that includes the code, tests, generated inputs, root build configuration, and declared project dependencies. Do not select all source to hide missing inputs. Ask the user when a dependency or scope is ambiguous.

## Preview and create

1. Use `workcopy_create` with a unique lane `name`, explicit `projects` or an owner-defined `profile`, and `dryRun: true`.
2. Review the full plan, selected paths, warnings, source bytes, storage headroom, and owner limits. A dry-run is not an apply token; Workcopy rechecks current state when applying.
3. If the user requested lane creation and the preview matches the request, repeat the same scope and selection with `dryRun: false`. Otherwise present the preview and wait for the user.
4. Verify the returned lane identity with `workcopy_lane_context` and `workcopy_status` before assigning work.
5. To include an omitted project later, preview `workcopy_expand` first; apply only after the new selection is understood.

## Coordinate writers

Create one uniquely named lane per independently writing agent. Workcopy provides scoped source and ownership/reconciliation primitives, not an execution scheduler. Use the host's existing agent/subagent facility when available. Assign exact write and excluded paths, test commands, output directories, dependency policy, and a human review owner. Do not assign overlapping files without naming one writer as owner.

Workers must confirm the exact lane path through `workcopy_status`. Host permissions and the host's own sandbox control filesystem access; Workcopy's selected projects are not a write allowlist. Keep build output inside the worker's lane. Never share mutable outputs between lanes by default.

## Recover safely

On busy, stale, low-space, or incomplete-operation errors, reread status and coordinate. Do not delete locks/journals, alter `.workcopy` metadata, or retry with broadened selection. Use `workcopy_recover` only for a Workcopy-recorded interrupted operation, then reread status. Preserve the lane and report any block that remains.

Use the server's tool descriptions and `workcopy://guide` for exact scope and pagination details. See [host orchestration](references/host-orchestration.md) for handoff fields.
