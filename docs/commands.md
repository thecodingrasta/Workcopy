# Command reference

`workcopy mcp [--repo DIRECTORY] [--root WORKSPACE-ROOT] [--config OWNER-CONFIG-FILE] [--max-lanes N] [--reserve-mib N] [--allow-share-target DIRECTORY ...]` starts the local stdio [MCP server](mcp.md). It uses protocol messages on stdin/stdout, not CLI `--json`. Tools accept explicit repository/root scope. Without `--repo`, the default root is a storage parent with a separate directory per repository. With `--repo`, the existing exact root remains the default. Owner configuration requires a default repository; ordinary CLI commands retain their contracts.

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
| `create NAME` | Create from committed source with `--branch`, `--profile`, repeated `--project`, `--all`, explicit `--config` or `--dry-run` |
| `expand NAME PATH...` | Add projects/paths; repeated `--project` is also accepted |
| `status NAME` | Show local source changes and durable conflicts; report shared-input warnings |
| `sync NAME` | Reconcile the saved selection with the locally resolved upstream ref |
| `resolve NAME PATH` | Resolve one token-bound conflict with exactly one content choice |
| `merge NAME` | Prepare an unstaged separate review; `--no-commit` is an alias |
| `merge NAME --commit` | Requires `--approve`, `--review` and `--message` |
| `destroy NAME` | Remove a clean owned lane; `--force` explicitly permits discarding owned changes |
| `list` | List registered lanes for this repository and workspace root |
| `discover` | Read committed project declarations; optional `--config` selects a local config |
| `solution NAME --solution FILE.sln` | Generate a `.slnf` beneath `.workcopy/views` without rewriting the solution or running a build |
| `share NAME` | Requires `--path`, `--target`, `--dependency-identity` and `--trust-shared` |
| `unshare NAME --path PATH` | Remove one managed link, not its target |
| `content NAME TOKEN` | Read `--side base`, `local` or `upstream` in base64 chunks |
| `recover NAME` | Revalidate and resume a recorded incomplete operation |
| `doctor` | Report installed runtime/Git and filesystem backend without modifying a repository |
| `legacy --workspace DIRECTORY` | Read-only metadata inventory; no adoption, conversion or deletion |
| `version` | Show build version, platform and Go runtime |

`resolve` requires `--expect-conflict TOKEN`. Its content choices are `--use-local`, `--use-upstream`, `--delete` and `--from-file FILE`. `--expect-local SHA256` acknowledges a newer local content version when necessary.

`content` defaults to an offset of 0 and a limit of 65,536 bytes. Limits must be positive and no greater than 65,536. The response includes the complete version's SHA-256, total length and `nextOffset`; `-1` means the end. Hash validation requires assembling the complete content, not hashing one chunk.

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
