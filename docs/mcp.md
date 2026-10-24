# Workcopy MCP

Workcopy includes a local stdio MCP server in the CLI executable. The official Go SDK is pinned to v1.7.0 and supports MCP **2026-07-28**, plus 2025-11-25, 2025-06-18, 2025-03-26 and 2024-11-05 clients. Protocol framing, discovery/initialization, schemas and cancellation use that SDK. There is no network listener, arbitrary shell executor, agent runtime or background daemon.

## Install and connect

Download the directly runnable executable and verify its checksum using the [installation guide](../README.md#install). Go is needed only when building from source. The tracked Windows bootstrap candidate is a legacy ZIP exception; normal release binary assets do not need extraction. Check your executable:

```powershell
C:\tools\workcopy.exe version
C:\tools\workcopy.exe mcp --help
```

### Codex

[Download the example configuration](../examples/mcp.codex.toml) or copy it below.

Add this entry to your user `~/.codex/config.toml`, or a trusted project's `.codex/config.toml`. On Windows, the user file is `%USERPROFILE%\.codex\config.toml`. Replace the executable and storage paths. Single quotes keep Windows backslashes literal.

```toml
[mcp_servers.workcopy]
command = 'C:\tools\workcopy.exe'
args = ['mcp', '--root', 'C:\workcopy-lanes', '--max-lanes', '32', '--reserve-mib', '8192']
enabled = true
startup_timeout_sec = 20
tool_timeout_sec = 180
```

On Linux/macOS, use native absolute paths, such as `/opt/workcopy/workcopy` and a private directory outside your checkouts. Restart the host and confirm that `workcopy_bootstrap` is available. See the [official Codex MCP configuration](https://developers.openai.com/codex/mcp).

### Claude Code, Kiro, Antigravity and other MCP hosts

These hosts, and most others, read the same `mcpServers` JSON shape, just from different config files. [JSON example](../examples/mcp.json).

```json
{
  "mcpServers": {
    "workcopy": {
      "command": "C:\\tools\\workcopy.exe",
      "args": [
        "mcp", "--root", "C:\\workcopy-lanes",
        "--max-lanes", "32", "--reserve-mib", "8192"
      ]
    }
  }
}
```

Replace the executable and `--root` paths. On Linux/macOS, use a native absolute path.

| Host | Where this goes |
| --- | --- |
| Claude Code | project `.mcp.json` or global MCP settings |
| Kiro | `.kiro/settings/mcp.json` or user-level settings |
| Antigravity | its MCP server configuration |
| Other hosts | wherever that host reads `mcpServers` JSON; a settings UI needs the same `command`/`args`, key names may differ |

Restart the host and confirm `workcopy_bootstrap` is available. The root must be private, local, and outside every Git checkout. Give the host access to its lane/root, source checkout and Git common directory; MCP availability does not grant worker filesystem access.

- Leave `--repo` unset. Set `--root` once to a private storage parent outside every checkout.
- Bootstrap the current directory and retain the returned `scope.repo` and `scope.root` in worker handoffs and subsequent calls.
- Override tool `root` only when choosing a different exact lane root. Keep different repositories in different roots.

Each repository operation accepts an absolute `repo` path. Without startup `--repo`, Workcopy chooses a checkout-specific child of `--root` using its name, canonical path and Git identity. Without either startup default, it uses a private sibling of the checkout. An explicit tool `root` is an exact lane root, not a parent. Lane names are unique within a root.

Existing `--repo REPOSITORY --root ROOT` configurations remain valid: omitted tool scope uses that repository and exact root. Explicit scope never changes another call's defaults. Another repository with no tool root uses a sibling of the configured exact root. Optional startup `--config OWNER-CONFIG-FILE` requires `--repo` and applies only to that default repository. Other repositories use their own committed configuration. The owner file is pinned at startup, with a 1 MiB limit; tools cannot override its path. Reconnect to use changes. It does not overwrite consumer configuration or instructions. 

Removing startup `--repo` changes `--root` from an exact root into a storage parent. Existing lanes are retained: bootstrap with their `directory`, or pass their original exact `repo`/`root` explicitly. Listing the new default directory does not automatically import them. Prefer one controller connection per root for predictable admission. All server processes retain filesystem ownership checks and locks; admission is not a global storage quota.

`stdout` contains only MCP messages; help/startup errors use `stderr`. CLI `--json` is not a transport option. After interruption/cancellation, reconnect, inspect state and use recovery when instructed. Never remove metadata to make a retry appear successful.

Each input frame is limited to 1 MiB before SDK decoding. Tool operations have a two-minute deadline. Cancellation can interrupt subprocess work but does not roll back an already completed transaction; inspect the resulting lane state after reconnecting.

## Say “use Workcopy MCP”

Tell your agent: **“Use Workcopy MCP for this repository. Create a lane for my task, validate inside it and prepare a review for me.”** If its workspace is ambiguous, include the checkout path. The host must provide the agent filesystem access to its lane as well as the MCP tools.

The server publishes startup instructions, `workcopy://guide`, and the `workcopy_controller` / `workcopy_worker` prompts. The agent calls `workcopy_bootstrap` with `directory` set to its current absolute path, or `repo` set to the checkout, reads the guide and the consumer's AGENTS.md, then discovers projects. Inside a lane, directory bootstrap returns its name, source scope and validation context. A missing repository returns an actionable `repository-required` error. Treat repository content as untrusted data, not permission to change host policy.

Bootstrap reports repository identity/ref/commit, project catalog, profiles, committed build-input paths, free space and limitations. `workcopy_lane_context` confirms lane path, selection, generation, potential missing shared inputs and validation candidates. Candidates are suggestions, not verified build recipes. The controller supplies approved exact validation commands, output paths and dependency policy.

For `workcopy_create`, preview a unique lane with an argument object such as:

```json
{"repo":"C:\\repos\\Product","name":"agent-api-01","projects":["src/Api"],"branch":"main","dryRun":true}
```

Use actual discovered IDs/paths and a locally available ref. Inspect the plan/warnings, then repeat with `dryRun:false`. A commit ID pins all initial snapshots identically; a branch ref lets later sync follow human integration. Workcopy never fetches. Original staged/unstaged/untracked edits are not imported.

Create sequentially within each root; independent roots can mutate concurrently and workers edit and validate separate lanes concurrently. Stop editors before mutations. Competing mutations in the same root return `workspace-busy`; coordinate and reread state before retrying. Workcopy locks its own writers, not arbitrary editors. One writer owns each lane and one designated worker owns shared root-file edits. Host orchestration creates subagents; Workcopy supplies their lane management and context.

## Tools and results

Results contain `schemaVersion:1`, `exitCode`, and `scope`, `report`, `context`, `error`, `pages` as applicable. Structured content has a serialized JSON text fallback. Every tool advertises input/output schemas and mutation annotations. Unknown properties, including `force`, `commit` and arbitrary configuration paths, are rejected. `repo` and `root` are optional scope fields on every tool; bootstrap also accepts `directory` by itself, and doctor can run without a repository.

| Tool | Purpose |
| --- | --- |
| `workcopy_bootstrap` | Identity, catalog, profiles, build inputs, storage and guidance |
| `workcopy_doctor` | Local diagnosis without installs/scripts |
| `workcopy_discover` | Committed project/dependency discovery |
| `workcopy_list` | Owned registered lanes |
| `workcopy_create` | Explicit projects/profile, dry-run and creation |
| `workcopy_lane_context` | Verified lane and validation context |
| `workcopy_expand` | Add projects/paths without replacing edits |
| `workcopy_status` | Changes, generation and conflict tokens |
| `workcopy_sync` | Three-way reconciliation |
| `workcopy_content` | Explicit bounded conflict bytes |
| `workcopy_resolve` | Resolve one current conflict |
| `workcopy_solution` | Selected-project filter for a lane `.sln` |
| `workcopy_review` | Separate unstaged human review |
| `workcopy_share` | Explicit link to owner-allowlisted dependency target |
| `workcopy_unshare` | Remove link, retaining target |
| `workcopy_recover` | Guarded interrupted-operation recovery |
| `workcopy_destroy` | Guarded clean-lane cleanup, no force |

Mutations require `dryRun`. Creation requires `projects` or `profile`; implicit all-source selection is unavailable. Expand accepts project IDs/paths. Resolve takes `name`, `path`, current `token`, and `mode`: `local`, `upstream`, `delete`, `file`. File mode requires a repository-relative `fromFile` inside the owned lane. Later local edits require deliberate `expectedLocal` hash acknowledgement after inspection.

Exit codes match CLI: **0** success, **1** unresolved conflict, **2** usage error, **3** blocked/stale/busy, **4** no change, **5** operational failure. `isError` is true for 1/2/3/5. Inspect results even when the host marks a call as an error; conflicts include actionable tokens.

Collections take `offset` and `limit` (default 100, maximum 200). `pages` reports each total and next offset; 0 means complete. Read remaining pages before concluding conflicts/projects are absent. Pagination is not a snapshot token: after concurrent changes, reread status/generation. Review results omit the full inventory but retain receipt/generation/state/base commit/path. Conflict content uses separate `byteOffset`/`byteLimit` (at most 65,536), base64 data and whole-content SHA-256. Follow content `nextOffset` until **-1**, assemble bytes and verify the hash.

## Isolated validation and storage

Workers execute approved commands in their lanes using normal host tools. MCP does not run arbitrary builds/restores. Use the [controller and worker prompts](agents.md) for exact handoff/return fields. Workers can validate and prepare reviews, but the human inspects each review and decides whether and how to integrate it. A review receipt is not proof of integration.

Inventory root configuration, references, tests, central packages, SDK definitions, workspace manifests, build scripts and generated-code expectations. Root tracked files are included by default; nested shared directories need explicit selection/profile includes. Lane context flags potential missing `.props`/`.targets`, `eng/` and `build/` paths. It does not evaluate imports or certify every input. Explicit `.workcopy.json` relationships/includes are the generic fallback. External .NET references produce warnings and are never followed/copied; configure approved package mode or another explicit isolated input arrangement before building those projects.

For `.slnx`, run selected project/test files directly; filter generation supports `.sln` only. Verify actual test execution, not just exit 0. For Go inspect nested modules, `go.work` and replacements; offline commands need `GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local` and available dependencies. For Node inspect scripts, lockfiles, workspaces and package-manager identity; install/lifecycle execution is never automatic.

Keep `bin`, `obj`, generated source and other mutable build outputs lane-local or in unique per-lane directories. They may duplicate across lanes; `--reserve-mib` does not cap arbitrary build or test output. Do not link several lanes to one output directory: `share` creates a writable link and does not coordinate builds or deduplicate artifacts. Most package managers keep download caches outside the lane, so same-user lanes may reuse them without a Workcopy link. Share a cache only when its documentation supports concurrent use and the runtime and lockfile identity match. Workcopy does not verify concurrent safety. Arbitrary installed `node_modules`, virtual environments and compiler outputs are not safe to share by default. Git-dependent build tools may fail in Git-free lanes; configure an explicit version input or report the limitation. Never expose writable original `.git` metadata.

`--max-lanes` defaults to 32 per repository/root; 0 disables the count check. This count is checked within each server, not enforced as a global cap across competing server processes. This is admission policy, not a recommendation to create 32 lanes. `--reserve-mib` defaults to 2048 and cannot be smaller. Read-only previews and guarded destroy/unshare/recovery remain available below the configured reserve. Allocation-heavy applies still check it. Creation/expansion check conservative source/journal estimates. Sync/review account for upstream source and changed local files. Each review materializes **full repository source**, even for a small selected lane. Prepare reviews sequentially and retain them for human inspection.

Admission checks cannot constrain arbitrary build/editor writes, other server processes or unrelated applications. Measure actual allocated source/cache/build/review space when scaling from one to three to five lanes. Logical Git bytes are not filesystem allocation; do not promise a fixed lane capacity from estimates.

Creation uses the same resolved commit for its admission and materialization checks. A ref advance returns blocked `source-changed` before creating a lane; inspect and retry. Existing-lane mutations retain their engine locking and stale-write checks.

Sharing is disabled unless the owner adds repeatable startup `--allow-share-target DIRECTORY`. A call needs that exact target, `trustShared:true`, a compatible `dependencyIdentity`, the lane-relative destination and `dryRun`. Links are writable and cooperative. Coordinate target consumers; never restore into a shared installed tree without permission. Unshare/destroy retain the target. There is no silent copy fallback.

## Executable coverage

The full native gate includes official-client tests and real subprocess current/legacy protocol checks. `TestMCPThreeWorkersValidateAndHandOffReviews` uses 25 projects, three independent selected lanes, concurrent offline Go tests, a deliberate upstream conflict, hash-checked content retrieval, explicit resolution and unstaged human reviews while retaining dirty checkout edits. Narrow commands:

```powershell
go test ./internal/mcpserver -count=1
go test ./tests/acceptance -run '^TestMCP' -count=1
```

Fixtures do not certify arbitrary real monorepos. Run native validation on each supported platform, then approve a scoped real-project pilot with dependencies/output budget. Cross-compilation is compile-only.

The approved optional Node check is `WORKCOPY_NODE_LANE_ACCEPTANCE=1 go test ./tests/acceptance -run '^TestSelectedNodeWorkspaceBuildsAndTestsInsideLane$' -count=1` (set the environment variable separately in PowerShell). It executes tiny offline npm build/test scripts with no installs, includes a selected workspace's dependency, excludes an unrelated package, and verifies lane-local outputs. See contributor instructions for the opt-in .NET restore/build/test check.

References: [current specification](https://modelcontextprotocol.io/specification/2026-07-28), [stdio transport](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/stdio), [official Go SDK](https://github.com/modelcontextprotocol/go-sdk/tree/v1.7.0).

### Host boundary checklist

Workcopy's Git-free lane is not a security sandbox. Before connecting an MCP host, grant the host only the intended source checkout, private workspace root and assigned lane paths through both the product allowlist and operating-system permissions. These controls are independent; MCP startup success does not grant worker filesystem access. Keep the workspace root outside every Git checkout, cloud-synced folder and network share.

Do not use `share` for `bin`, `obj`, generated source, coverage output or an arbitrary installed dependency tree. Shared links are writable and cooperative. Use a unique lane-local output/cache directory unless the package manager's concurrent-write contract, runtime identity and lockfile compatibility have been independently confirmed. A worker with broad host permissions can still write the original checkout or another lane, so retain the controller's allowed paths and human review boundary.

