---
name: workcopy-safe-review
description: Inspect Workcopy lane changes and conflicts, collect validation evidence, and prepare a separate human review. Use before handing lane work back or reconciling upstream changes.
license: GPL-3.0-or-later
---

# Workcopy Safe Review

MCP hosts may prefix tool names with the server name. Match the Workcopy tool by its full suffix; for example, OpenCode adds the server name to `workcopy_bootstrap`.

The goal is an accurate human handoff. A single-lane review is separate and unstaged. For multiple lanes, prepare one combined batch review for the owner's chosen subset.

## Inspect

1. Stop or coordinate with any editor still writing to the lane.
2. Call `workcopy_status` with the registered lane name and exact `scope.repo`/`scope.root`. Read every page and note the lane generation, changed paths, warnings, and current conflict tokens.
3. Run only validation commands supplied or approved for the task using the host's normal tools in the lane. Record each exact command, working directory, exit status, and relevant test count. Workcopy MCP does not execute builds, installs, or arbitrary commands.

## Reconcile when requested

1. Call `workcopy_sync` with `dryRun: true` to inspect the candidate update. Apply sync only if the user requested reconciliation and editors are stopped.
2. For each conflict, use its current token and inspect base/local/upstream content through bounded `workcopy_content` calls. Request every chunk through `nextOffset == -1`, assemble the bytes, and verify the full SHA-256 before relying on content.
3. Choose a resolution from inspected evidence. If the correct result is unclear, ask the user. Call `workcopy_resolve` for one path with the current conflict token and exactly one supported mode. Preview when available, then reread status and preserve all remaining conflicts.
4. On a stale token, busy root, or changed local bytes, stop and reread. Never recycle an old token or acknowledge a new local hash without inspecting the bytes.

## Prepare the human handoff

1. Call `workcopy_review` with `dryRun: true` and inspect effects/storage warnings.
2. If the user requested review preparation and the preview is appropriate, apply with `dryRun: false`.
3. Report the review path and receipt, changed paths, validation evidence, unresolved conflicts, and any blockers. Tell the human to inspect the separate diff and decide whether/how to integrate.

For a batch, call `workcopy_batch_review` with the ordered `names`, local `branch`, and `dryRun`. Resolve conflicts with current tokens, then validate and present the base-to-candidate diff and batch ID. After explicit approval of that ID, pass the returned `batchLandArgs` and then `--approve` as separate arguments to the authorized local Workcopy executable. The equivalent is `workcopy batch-land BATCH-ID --repo REPO --root ROOT --approve`, using the exact returned `scope.repo` and `scope.root`. Use the same scope with `workcopy batch-recover BATCH-ID --repo REPO --root ROOT` if recovery is needed.

Never call commit, push, Git checkout/reset, or any main-branch integration command from a lane. The MCP server does not expose a force-discard option. Do not bypass a guard by switching to the original checkout or editing Workcopy metadata.

See [conflict handling](references/conflict-handling.md) for pagination and recovery details.
