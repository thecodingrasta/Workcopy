---
name: workcopy-multi-agent-orchestration
description: Coordinate independent writers using one explicit Workcopy lane per host-managed agent. Use when a user requests parallel work across separate repository areas.
license: GPL-3.0-or-later
---

# Multi-Agent Orchestration with Workcopy

MCP hosts may prefix tool names with the server name. Match the Workcopy tool by its full suffix; for example, OpenCode adds the server name to `workcopy_bootstrap`.

Workcopy supplies lanes, status, synchronization, conflict, recovery, and review primitives. The host supplies agent scheduling and execution. Do not imply Workcopy launches or supervises workers.

## Plan the work

1. Use `workcopy_bootstrap` and `workcopy_discover` to confirm the repository, committed source ref, project graph, and storage headroom.
2. Divide work into independent write scopes. Identify shared files and assign one owner for each. If work cannot be separated, use a single writer.
3. Use the host's documented agent/subagent workflow. Verify that each worker can access only its assigned lane to the degree the host/OS supports; Workcopy selection is not an access-control boundary.
4. For each worker, preview a uniquely named `workcopy_create` with explicit project/profile selection and exact returned scope. Review the preview and create lanes sequentially from the same unchanged source ref. Never create a lane implicitly as setup.
5. Hand off repository identity, lane path/root, selected projects, allowed/excluded write paths, exact test commands, dependency/network policy, build output locations, conflict owner, and human review owner.

## Run and monitor

Have each worker call `workcopy_lane_context` and `workcopy_status` before edits and verify the returned path. Assign one writer per lane. Use `workcopy_status` for observation; do not sync, resolve, refresh, or delete while a worker is editing.

Workers use only their host-approved validation commands and report exact command, working directory, exit code, and test count. They must not run installs/restores unless the handoff permits them. Build outputs remain unique to each lane. Workcopy does not execute these commands.

## Reconcile and hand off

After writers stop, inspect each completed lane's full status and page through collections. Ask which lanes the owner wants to land. Use `workcopy_batch_review` with that ordered subset and the target branch. Resolve reported conflicts with current tokens, validate the combined candidate, and present its diff and batch ID. After explicit approval for that ID, pass the returned `batchLandArgs` and then `--approve` as separate arguments to the authorized local Workcopy executable. The arguments retain the exact repository and lane root. Report the resulting branch tip.

Destroy lanes only after the owner confirms integration or authorized discard, clean status, and a passing guarded preview. Use `workcopy_recover` only for a recorded interrupted operation. Preserve blocked lanes.

Each handoff must include the repository, lane identity, selected projects, write boundaries, validation commands, dependency policy, conflict owner, and review owner.
