# Working with a lane

## Create and expand

Run from the original repository:

```sh
workcopy create fix-parser --all
workcopy status fix-parser
```

Edit the printed lane directory. It is ordinary editable source, not a Git checkout. Creation reads the chosen commit even when the original checkout has staged, unstaged or untracked work. `--branch feature` selects that committed branch, not the current checkout's bytes.

`workcopy expand fix-parser src/Extra` adds a project directory. Existing local edits remain in place. Root files and the lane's saved selection rules remain consistent across status, sync and review.

## Agent instructions

Each new lane receives a private copy of your untracked agent instruction files (rules, steering, prompts, skills) so an agent in the lane works with the project's guidance. They are not source: `status`, reviews and commits ignore them, and `destroy` discards them. `agent-context --refresh` (and `sync`) bring them up to date. See [agent context](agent-context.md).

## Line endings and file modes

A lane is written the way the repository's own checkout would be written, per path: `.gitattributes` (`text`, `eol`, `crlf`) at the pinned commit decides, then `core.autocrlf` and `core.eol` from your Git configuration. In a repository with `*.cs text eol=crlf`, `.cs` files are CRLF in the lane even when the Linux build creates it; in an LF repository they stay LF. The decision table is a port of Git's own and is tested byte for byte against `git checkout`.

Workcopy compares, hashes and merges the LF form Git stores. An editor that rewrites line endings therefore changes nothing, cannot turn an upstream edit into a conflict, and does not show as a modification. New files follow the same rules, so a CRLF file created by Visual Studio in an `eol=crlf` repository is committed as LF. A blob that already contains a carriage return is left exactly as committed, because converting it could not be reversed; Git itself reports such files as needing renormalisation.

Execute bits follow `core.fileMode`. A lane does not track them when the repository sets `core.fileMode=false`, on Windows, or when the lane's filesystem reports every file as executable (WSL's `/mnt/c` without metadata, FAT). Workcopy detects the last case itself, including for lanes created by v0.1.3.

## Sync

```sh
workcopy sync fix-parser
```

Workcopy uses the locally available upstream ref. Fetch in the original repository yourself when you want newer remote commits. Workcopy never performs network Git operations.

For each selected path, reconciliation compares the accepted upstream baseline, local lane content and new upstream content. Independent text edits are merged. Binary, add/delete, file-type and overlapping text conflicts remain unresolved until chosen explicitly.

A conflict does not overwrite local bytes. `status --json` retains the conflict record and token across processes. Sync does not hide conflicts because another file was resolved or a global commit cursor advanced.

## Resolve

Read the conflict token from `status`. Choose exactly one of `--use-local`, `--use-upstream`, `--delete` or `--from-file`. The command syntax is:

```text
workcopy resolve NAME PATH --expect-conflict TOKEN --use-local
workcopy resolve NAME PATH --expect-conflict TOKEN --from-file RESOLVED-FILE
```

These uppercase terms are command arguments: `NAME` is the lane, `PATH` is repository-relative, `TOKEN` comes from status and `RESOLVED-FILE` is a file you reviewed. Do not invent tokens.

When the local file changed after the conflict was recorded, resolution also requires `--expect-local` with the SHA-256 of its current bytes. Review those bytes before acknowledging that hash.

After resolution, the upstream version becomes the accepted baseline. The chosen local result stays a contribution when it differs from upstream. Zero unresolved conflicts does not mean there is nothing to promote.

## Prepare a review

```sh
workcopy merge fix-parser
```

This reconciles locally available upstream first. It then creates a full Git review worktree containing committed source and your contribution. The review has a detached HEAD, an unchanged baseline index and unstaged edits. `--no-commit` is an explicit compatibility alias for this default.

Review worktrees contain full tracked source, not only the active lane's selection. Ignored packages and caches are not copied or linked into the review. The original checkout's HEAD, index and human edits are not changed.

Inspect the review with Git, including untracked additions. A prepared review is an exported copy, not proof of integration.

## Commit and integrate

After reviewing the exact prepared contents, request an explicit commit:

```text
workcopy merge NAME --commit --approve --review RECEIPT --message MESSAGE
```

Workcopy checks the lane generation, review content, index and detached HEAD. It then commits through an isolated temporary index. It does not run Git hooks, sign the commit, attach a branch or push. Git must know an author and committer: set `user.name` and `user.email`, or export `GIT_AUTHOR_NAME`, `GIT_AUTHOR_EMAIL`, `GIT_COMMITTER_NAME` and `GIT_COMMITTER_EMAIL` for the Workcopy process. Without one the command stops with `git-identity-missing` before anything is written, and the same receipt can be approved again afterwards ([details](troubleshooting.md#merge-says-git-has-no-identity)).

A changed review is not silently overwritten or re-approved. Use normal Git to manage a deliberately edited review yourself, or preserve it and prepare a new lane contribution. Workcopy's approval path is for unchanged receipts.

Integrate the resulting commit using your normal review process. For example, a human may cherry-pick the commit onto the intended branch. Workcopy does not execute that integration. After the upstream ref contains the contribution, sync the lane again; it can then become clean.

## Delete

```sh
workcopy destroy fix-parser --dry-run
workcopy destroy fix-parser
```

Ordinary deletion refuses local changes, deleted source files, conflicts and local-only files, including ignored files. An owner who wants build output to be disposable lists it under `destroy.discard` in an owner-supplied `--config` file (see [configuration](configuration.md)); only untracked files matching those patterns are then discarded without `--force`. A previously exported or committed review does not bypass this check. Only use `--force` after independently preserving anything valuable and explicitly deciding to discard the lane.

Deletion quarantines the owned lane, inventories its entries and removes links rather than their targets. Recovery does not broaden an earlier deletion decision to cover newly created files. Separate review worktrees remain available and must be managed through ordinary Git worktree commands when no longer needed.

### A lane whose directory was deleted

If a lane directory is removed outside Workcopy, `list` shows it with phase `missing`, and `status`, `sync` and `recover` fail with `workspace-missing` naming the directory and the registration file. `workcopy destroy NAME` clears it: it removes the registration and Workcopy's `refs/workcopy` pins for that lane and nothing else. Review worktrees are kept and listed with the exact `git worktree remove` command for each. `--dry-run` shows the same without changing anything.

## Interrupted operations

```sh
workcopy recover fix-parser --dry-run
workcopy recover fix-parser
```

Recovery verifies recorded ownership and before/after states. It resumes a pending creation, transaction, review preparation, review commit or deletion. Later edits that do not match the recorded operation block replay. A process interrupted between two distinct journalled stages may need another `recover` invocation; a no-op returns exit code 4.

If a review was interrupted and its half-built worktree has since been edited, `recover` refuses to overwrite it and the lane stays blocked. `workcopy recover NAME --abandon-review` (CLI only, not offered over MCP) drops that unfinished receipt so the lane works again. It deletes no files; remove the leftover review with the `git worktree remove` command it prints, then run `merge` for a fresh review. A review that reached `prepared` or beyond is never abandoned this way.

Do not remove journals or registry files to clear an error. Preserve the complete workspace root and consult [troubleshooting](troubleshooting.md).
