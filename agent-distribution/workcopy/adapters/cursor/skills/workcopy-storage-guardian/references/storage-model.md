# Storage model

Workcopy copies selected committed source and maintains versioned lane state, journals, content objects, conflict data, pins, and a separate full-source Git worktree for a prepared review. Successful transactions collect unreferenced objects; interrupted plans can retain staging until recovery or a later successful mutation. Review preparation may need a full repository copy even when a lane selected only a small project.

MCP bootstrap/doctor report free disk and Workcopy admission estimates. They do not report all allocated lane bytes or cap arbitrary build/test/editor writes. Measure actual directories only through the host's read-only filesystem tooling and within the path returned by Workcopy. Keep build output and dependency caches lane-local unless the owner explicitly approves a compatible, concurrent shared target through `workcopy_share`.

`workcopy_destroy` is guarded and has no MCP force option. It refuses unsafe ownership, local-change, conflict, and incomplete-operation states. `workcopy_unshare` removes the owned link but retains its target. Neither should be bypassed with direct file deletion or edits to `.workcopy` and workspace registry state.
