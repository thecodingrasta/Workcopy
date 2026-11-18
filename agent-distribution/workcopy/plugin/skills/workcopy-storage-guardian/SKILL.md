---
name: workcopy-storage-guardian
description: Inspect Workcopy storage headroom, lane ownership and cleanup blockers, then preview guarded cleanup. Use when disk capacity is low or lanes may be reclaimable.
license: GPL-3.0-or-later
---

# Workcopy Storage Guardian

MCP hosts may prefix tool names with the server name. Match the Workcopy tool by its full suffix; for example, OpenCode adds the server name to `workcopy_bootstrap`.

Use Workcopy reports for ownership and safety state. Do not assume that a lane is disposable because it is old, idle, or large.

## Inspect storage

1. Call `workcopy_bootstrap` and `workcopy_doctor` to read available bytes, Workcopy reserve, repository source estimate, warnings, and current scope. These are not a disk quota or a measurement of every editor/build output.
2. Call `workcopy_list` and read all pages to identify registered lane paths, repository identity, and lifecycle phase.
3. For candidate lanes, call `workcopy_status` and `workcopy_lane_context`. Explain changed files, unresolved conflicts, incomplete operations, review state, shared-target notes, and source selection before recommending cleanup.
4. Call `workcopy_storage` for a lane's point-in-time allocation, reclaimable and shared bytes, and volume capabilities. Check the report for unreadable files and block sharing before interpreting the totals. The measurement covers the lane and leaves link targets and outputs elsewhere untouched.

## Decide what is protected

- Local contributions, unresolved conflicts, active work, incomplete operations, and unreviewed material require preservation and owner review.
- A separate review folder can contain the human handoff. Keep it until the owner finishes inspecting it.
- Shared dependency targets are retained by `workcopy_unshare` and `workcopy_destroy`; unshare removes only the owned link.
- Workcopy's free-space admission checks do not limit arbitrary build or test output. Do not claim that cleanup candidates are safe based on age or free-space counts alone.

## Preview and cleanup

1. Ask the owner whether the contribution has been integrated or may be discarded. Do not infer that from a clean lane.
2. Use `workcopy_destroy` with `dryRun: true` and exact repository/root scope. Review the plan, warnings, protected files, and effects.
3. Apply `workcopy_destroy` only after explicit user authorization and only when Workcopy's ownership/safety checks allow it. The MCP tool has no force option. Never invoke CLI force, broad deletion, direct filesystem deletion, or manual metadata changes to make cleanup pass.
4. For an owned dependency link the user wants removed, preview `workcopy_unshare`; confirm the target will be retained; then apply only when requested.
5. Reread `workcopy_list` and status after cleanup. Report what Workcopy removed and what remains.

If any state is busy, stale, conflicting, incomplete, or unclear, preserve it and report the exact blocker.

See [storage model](references/storage-model.md) for limits and expected retained data.
