# Workcopy tool selection

Start with the actual tools exposed by the connected MCP server. Tool names and arguments are versioned interfaces; never invent a tool, omit required fields, or assume another host's server is connected.

| Need | Existing MCP tools |
| --- | --- |
| Read-only setup and discovery | `workcopy_doctor`, `workcopy_bootstrap`, `workcopy_discover` |
| Current lane identity and state | `workcopy_list`, `workcopy_lane_context`, `workcopy_status` |
| Measure one lane's storage | `workcopy_storage` with the lane's name and scope |
| Explicitly scoped lane changes | `workcopy_create`, `workcopy_expand`, `workcopy_sync`, each with `dryRun` first |
| Conflict inspection/resolution | `workcopy_content`, `workcopy_resolve` with the current token |
| Separate human review | `workcopy_review`, with a preview before applying |
| Combined review for selected lanes | `workcopy_batch_review` with ordered `names`, local `branch`, and `dryRun` |
| Interrupted operation recovery | `workcopy_recover` |
| Guarded dependency link lifecycle | `workcopy_share`, `workcopy_unshare` |
| Guarded cleanup | `workcopy_destroy` with a preview first |
| Copied agent instructions | `workcopy_agent_context`, `workcopy_refresh_context` |

Read `workcopy://guide` for exact scope, pagination, content hashing, ownership, storage, conflict, and human-review details. Preserve `scope.repo` and `scope.root` returned by bootstrap for subsequent lane calls. The `workcopy_solution` tool generates a selected-project `.slnf`; it does not run or certify a build.
