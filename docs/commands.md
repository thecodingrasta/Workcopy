# Command reference

`workcopy mcp [--repo DIRECTORY] [--root WORKSPACE-ROOT] [--config OWNER-CONFIG-FILE] [--max-lanes N] [--reserve-mib N] [--tool-timeout DURATION] [--allow-share-target DIRECTORY ...]` starts the local stdio [MCP server](mcp.md). It uses protocol messages on stdin/stdout, not CLI `--json`. Every option below can instead be set once in the [settings file](settings.md) (for example `WORKCOPY_TOOL_TIMEOUT=20m`); a flag overrides it. `--tool-timeout` sets the per-call deadline (default 2m, allowed 10s to 24h). Tools accept explicit repository/root scope. Without `--repo`, the default root is a storage parent with a separate directory per repository. With `--repo`, the existing exact root remains the default. Owner configuration requires a default repository; ordinary CLI commands retain their contracts.

Run `workcopy --help` for syntax. Flags can appear before or after positional arguments. Values can use `--flag value` or `--flag=value`. Boolean flags do not accept `=true` or `=false`.

## Context and output

| Option | Purpose |
| --- | --- |
| `--repo DIRECTORY` | Original Git checkout or linked Git worktree |
| `--root DIRECTORY` | Workspace storage outside every Git checkout; `--work-root` is an alias |
| `--workspace DIRECTORY` | Read an existing lane's recorded context |
| `--name NAME` | Explicit lane name, including when inside another lane |
| `--json` | One schema-versioned JSON object on stdout |
| `--dry-run` | Plan a mutation without changing state, refs, files or logs |

Inside a lane, repository, root and name are inferred. Omit the positional lane name in that context, or use `--name` explicitly. Relative source paths use `/` on every platform. Shell quotes are still required around values containing spaces.

Dry-run is candidate planning, not an apply token. Applying re-reads and validates current state. A create dry-run requires a destination name that is not already registered or present; use an unused temporary name for a later preflight. It will not inspect or replace an existing lane. Sync dry-run does not create temporary files to predict text-merge outcomes. Read-only commands are already non-mutating.

## Commands

| Command | Behaviour |
| --- | --- |
| `create NAME` | Create from committed source with `--branch`, `--profile`, repeated `--project`, `--all`, explicit `--config`, `--agent TOOL` (whose instruction files the lane serves; repeat or comma-separate) or `--dry-run` |
| `expand NAME PATH...` | Add projects/paths; repeated `--project` is also accepted |
| `status NAME` | Show local source changes and durable conflicts; report shared-input warnings |
| `sync NAME` | Reconcile the saved selection with the locally resolved upstream ref |
| `resolve NAME PATH` | Resolve one token-bound conflict with exactly one content choice |
| `merge NAME` | Prepare an unstaged separate review; `--no-commit` is an alias. `--json` leaves out the per-file inventory (`review.files`, megabytes on a large repository) unless `--include-files` is given |
| `merge NAME --commit` | Requires `--approve`, `--review` and `--message` |
| `destroy NAME` | Remove a clean owned lane; `--force` explicitly permits discarding owned changes. Local-only files matching the owner's `destroy.discard` patterns (build output) never need `--force`. On a lane whose directory was deleted outside Workcopy it clears the registration and Workcopy's pins, deletes nothing else, and prints the `git worktree remove` command for each retained review A lane whose creation never finished (phase `creating`, which `recover` cannot complete) is discarded with `destroy NAME --force`, which removes its partial directory, registration and pinned ref. |
| `list` | List registered lanes with name, path, branch and phase (`ready`, `creating`, `deleting` or `missing`) |
| `discover` | Read committed project declarations; optional `--config` selects a local config |
| `solution NAME --solution FILE.sln` | Generate a `.slnf` beneath `.workcopy/views` without rewriting the solution or running a build |
| `share NAME` | Requires `--path`, `--target`, `--dependency-identity` and `--trust-shared` |
| `unshare NAME --path PATH` | Remove one managed link, not its target |
| `content NAME TOKEN` | Read `--side base`, `local` or `upstream` in base64 chunks |
| `recover NAME` | Revalidate and resume a recorded incomplete operation. `--abandon-review` drops the receipt of a review whose preparation never finished and cannot be resumed (for example because someone edited it); it deletes no files and prints how to remove the leftover review. Only receipts still in `preparing` are affected |
| `settings` | Show every effective setting and its source (`--json` for machines). `--init` writes a commented `workcopy.env` template to the per-user location, or to `--settings FILE`, and never overwrites. See [settings](settings.md) |
| `agent-context [NAME]` | Without a name, preview which untracked agent instruction files (rules, steering, prompts, skills) a new lane would receive, what was refused and why, and unrecognised candidate directories. With a lane name, show what a refresh would change; `--refresh` applies it (copies edited in the lane are kept); `--agent TOOL` previews or switches the tools a lane serves. See [agent context](agent-context.md) |
| `doctor` | Report the runtime, the Git program in use (and `WORKCOPY_GIT` if set), the repository's line-ending and file-mode settings, and warnings such as a repository on a WSL Windows drive. Accepts `--repo` and `--root`; modifies nothing |
| `legacy --workspace DIRECTORY` | Read-only metadata inventory; no adoption, conversion or deletion |
| `version` | Show build version, platform and Go runtime |

`resolve` requires `--expect-conflict TOKEN`. Its content choices are `--use-local`, `--use-upstream`, `--delete` and `--from-file FILE`. `--expect-local SHA256` acknowledges a newer local content version when necessary.

`content` defaults to an offset of 0 and a limit of 65,536 bytes. Limits must be positive and no greater than 65,536. The response includes the complete version's SHA-256, total length and `nextOffset`; `-1` means the end. Hash validation requires assembling the complete content, not hashing one chunk.

## Environment

| Variable | Effect |
| --- | --- |
| Any `WORKCOPY_*` key | Every setting in [settings](settings.md) can also be an environment variable; the flag beats the variable, the variable beats the settings file |
| `WORKCOPY_GIT` | Path or name of the Git program to use instead of the first `git` on `PATH`. A value that does not resolve fails with `git-unavailable` rather than falling back. Useful under WSL when the checkout belongs to Windows Git |
| `WORKCOPY_IO_WORKERS` | Number of concurrent file operations for bulk work (default: twice the CPU count, 4 to 16). `1` makes bulk operations strictly sequential for diagnosis |

Paths for the other operating system are rejected with exit 2 and the fix: `posix-path-on-windows` when a Windows build is given `/mnt/c/...` (use `C:\...`), `windows-path-on-posix` when a Linux or macOS build is given `C:\...` (use `/mnt/c/...` under WSL).

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Completed successfully |
| 1 | Unresolved conflicts remain |
| 2 | Invalid command usage |
| 3 | Ownership, approval, busy, stale or another safety precondition blocks progress |
| 4 | No change was needed |
| 5 | Operational failure |

A JSON response includes `schemaVersion`, `exitCode` and `command`, plus relevant `workspace`, `changes`, `conflicts`, `review`, `content`, `plan`, `warnings` or `error` fields. Optional empty fields may be omitted. Do not parse human text for decisions. See the [response schema](../schema/response.schema.json).

The process exit code matches the JSON value when output succeeds. A broken output stream returns an operational failure instead of claiming a response was delivered.

## Compatibility

The Bash command names remain available. Safer semantics deliberately differ: snapshot-based source, review-first merge, token-bound resolution and ownership-checked deletion. Legacy create-time sharing flags are rejected with an explicit error; use `share` after creation. Old Bash metadata is never upgraded in place.
