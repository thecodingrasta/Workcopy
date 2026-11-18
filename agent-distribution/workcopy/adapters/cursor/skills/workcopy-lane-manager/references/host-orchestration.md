# Host-owned agent handoff

Workcopy does not spawn or schedule agents. Use only the host's established agent/subagent facilities and keep their workspace path set to the assigned Workcopy lane.

Give each worker:

- source repository identity and source ref;
- lane name, exact absolute path, root, and selected project IDs/paths;
- allowed and excluded write paths;
- exact build/test commands and working directories;
- whether network restore is allowed and the isolated cache/output locations;
- who may reconcile conflicts and who owns human review.

Require each worker to verify its assignment with `workcopy_lane_context` and `workcopy_status`, edit only the assigned paths, report exact validation commands and exit statuses, and return changed paths, blockers, remaining conflict tokens, and review receipt/path if prepared. Make no claim that Workcopy itself ran a build or isolated filesystem writes.

Do not share `bin`, `obj`, `node_modules`, generated code, coverage, virtual environments, or writable package stores across lanes. The `workcopy_share` tool requires the exact owner-allowlisted directory, explicit `trustShared`, a dependency identity, and a dry-run; it creates a writable cooperative link, not a safe cache or lock.
