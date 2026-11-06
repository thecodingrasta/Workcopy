# Troubleshooting

## The command is not found

Run `go run ./tools/dev install` from the source directory. Check `go env GOBIN GOPATH` and add the applicable `bin` directory to your user `PATH`. Open a new terminal after editing `PATH`. Alternatively run the binary in `dist` directly.

## Git or attributes are unsupported

Use Git 2.43 or newer. `doctor` reports the installed executable's version, while source materialization checks attribute support. Workcopy rejects external filters, LFS filters, `working-tree-encoding` and `ident` rather than running repository-configured commands. Use a supported repository layout or exclude irrelevant affected source explicitly.

## A lane-only command fails outside any Git repository

`destroy`, `status`, `sync`, `recover` and the other lane commands still resolve a repository context even when `--root` alone looks sufficient. Without `--repo`, Workcopy falls back to the current directory; outside a lane and outside a Git checkout, that fails with a raw Git error such as `not a git repository (or any of the parent directories): .git`. Run the command from inside the lane, from inside the original checkout, or pass `--repo` explicitly alongside the registered lane's `--root` and name.

## A selected project does not build

Workcopy did not run a build. Check the profile's projects, shared imports, SDK/runtime and dependency configuration. Use `discover` warnings and explicit dependencies for relationships that cannot be found declaratively. For .NET, generate and inspect a solution filter rather than building a full solution containing absent projects.

`workcopy solution` accepts `.sln` files and writes a `.slnf`; it does not evaluate MSBuild conditions or prove that the filter builds. Run `dotnet build` and `dotnet test` against the returned filter path from the lane, using a configuration actually declared by the solution. If MSBuild reports `MSB4121` or builds no projects, inspect the solution's configuration mappings and choose a valid configuration. A successful command that reports zero executed tests is not test evidence. If the repository only has `.slnx`, build and test the selected `.csproj` directly or maintain a supported `.sln`.

Check `Directory.Build.props` and `Directory.Build.targets` for output paths that point outside the lane. Standard project-local `bin` and `obj` paths remain inside the copied lane; custom absolute paths can write elsewhere. Keep generated outputs ignored in the lane and confirm `workcopy status --json` after the build.

## A workspace root is rejected

Choose a private local directory outside the original checkout and outside every ancestor containing `.git`. Do not use a shared network drive, a cloud-synchronised directory or a symlink to bypass this boundary. The source repository must remain available at its registered path.

## Discovery or preflight reports access denied

`discover` reads committed source through Git and validates the workspace-root path boundary; it does not create the workspace root. The calling process needs traversal/read access to the source checkout and its Git common directory, plus access to inspect the workspace-root path and its ancestors. An agent host may deny those paths even when the same Windows account can access them in an ordinary terminal. Check both the operating-system permissions and the host's project/workspace filesystem allowlist. They are separate controls. Do not change global Git safety settings or move the root inside a checkout to work around the denial. If the command still fails in the configured host context, capture the sanitized command, OS, `doctor` output and error for the Workcopy maintainer.

## An agent cannot access a lane

The lane is a local directory on the machine running Workcopy. A remote or cloud agent cannot use that path unless its execution environment provides the same filesystem. For a local agent, two independent checks may be required:

1. The agent product/project must allow the absolute workspace-root and lane paths. If the agent also runs Workcopy commands that read or sync the source, allow the source checkout and Git common directory too. Workcopy cannot change another product's sandbox or allowlist.
2. The operating-system ACL must grant the process's actual run-as account or group access. On Windows, new directories inherit ACL entries from their parent by default ([Microsoft `CreateDirectory` documentation](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createdirectory)); Workcopy does not add vendor-specific groups automatically.

On Windows, inspect the root's current permissions with `icacls`. If needed, have the directory owner grant Modify to the correct account/group, replacing the example paths and principal with values from that host:

```powershell
icacls "C:\path\to\repository.workcopy" /grant "HOST\AgentSandboxUsers:(OI)(CI)M"
```

`M` means Modify; `(OI)(CI)` makes the grant inheritable by files and subdirectories, as described in Microsoft's [`icacls` reference](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/icacls). Replace `HOST\AgentSandboxUsers` with the actual execution identity or group. An inheritable grant on a shared root gives every member of that group access to every lane under it. Use a narrower grant or the host's per-project sandbox policy when lanes must remain isolated. Whitelisting a directory in the agent host does not grant an NTFS ACL, and granting an ACL does not whitelist the directory in the agent host. Configure both when required, then have the agent run `workcopy status --json` from its assigned lane. Do not grant broad access to `Everyone` or disable the host sandbox to work around a denied path.

## A directory link fails on Windows

The target must be an existing local-drive directory on a filesystem supporting junctions. UNC paths are rejected. Check filesystem permissions and that the destination does not already exist. A failed mapping leaves a recovery record instead of silently copying packages. Correct the prerequisite and run `recover`.

Source symlinks are different from junctions. Creating them may require Windows Developer Mode or the relevant account privilege. Do not replace them with ordinary files to make tests appear green.

## Workspace busy

Another process holds the operating-system writer lock. Wait for that known process to finish or stop your own task-owned process. The lock is released when its handle closes. The small lock file can remain after a successful operation; its existence alone does not mean a writer is active. Do not delete it to obtain a second lock inode.

## Recovery required

Run `recover --dry-run`, then `recover` for the same lane and root. A transaction replays only exact before/after states. A changed file or review index is retained and blocks replay. Preserve those edits independently before deciding how to reconcile the interrupted operation. Do not hand-edit the journal.

After a partial deletion, use the registered lane name with `--repo` and `--root` from outside the old lane. Do not rename the quarantine or run a recursive delete over it.

## Review was changed

The approved path checks exact files, ignored additions, staging and detached HEAD. Workcopy does not overwrite a human-edited review. Continue managing that review with ordinary Git, or preserve its contents and return the lane to the intended contribution before preparing another review. Do not attach a review to the main branch and retry Workcopy's commit command.

## Destroy refuses a clean-looking lane

`status` reports selected source; deletion additionally protects ignored and local-only files. A package restore, editor file or local note may still need preserving. A review export alone is not proof of integration. Use `--force` only as an explicit decision to discard all remaining owned lane contents.

## A lane directory was deleted, or a review is stuck

`list` shows a lane whose directory is gone with phase `missing`; `status` and `recover` say `workspace-missing`. Run `workcopy destroy NAME` to clear its registration and pins; review worktrees stay and are printed with a `git worktree remove` command. A review stuck in `preparing` that `recover` refuses to resume (someone edited it) is released with `workcopy recover NAME --abandon-review`, which deletes no files. See [workflow](workflow.md).

## Every file shows as modified, or review preparation fails with "Review content changed"

Seen with v0.1.3 under WSL on `/mnt/c`: the mount reported every file as executable, so every file looked locally modified. Current versions detect this when the lane is created and also repair existing lanes when they are next read. If you still see mass modifications, check `workcopy doctor` and `core.fileMode`, and make sure a lane is not being edited by a tool that rewrites file modes.

## Line endings differ from my checkout

A lane follows `.gitattributes` and your Git configuration the way `git checkout` would (see [workflow](workflow.md)). Workcopy reads `core.autocrlf` and `core.eol` from the Git program it runs, so under WSL the Linux Git's settings apply, not Windows Git's. `workcopy doctor` prints the values in effect; set them for that Git, or choose the program with `WORKCOPY_GIT`.

## WSL: Windows repositories, slow operations, worktrees Visual Studio cannot open

The full explanation, a decision table and the measurements are on [Windows, WSL and /mnt/c](wsl.md). In short:

**The fix for slow lanes is where they are stored.** Keep lane storage on the WSL filesystem even when the repository stays on `/mnt/c`: set `WORKCOPY_ROOT=/home/you/.workcopy/lanes` in the [settings file](settings.md). On a repository of 1,000 files this took lane creation from 6.4 s to 0.46 s, review from 8.8 s to 0.75 s and `status` from 1.1 s to 0.05 s ([measurements and the reason](validation.md#why-mntc-is-slow-researched)). Microsoft gives the same advice: for the fastest performance, store files in the WSL file system when working from a Linux command line. `workcopy doctor` and `workcopy create` warn when lane storage is on a Windows drive.

Use the Windows `workcopy.exe` for a repository checked out on Windows when Windows tools must see the lanes; give it Windows paths (`wslpath -w`). The Linux build on `/mnt/c` (drvfs/9p) works but is slow, the review worktree it registers with Linux Git is invalid for Windows Git (`git worktree list` marks it `prunable`, and `git gc` can drop its metadata), and a symlink made by `share` is unreadable from Windows. `workcopy doctor` warns about this combination. A repository on the WSL filesystem (`~/src`) with lanes beside it avoids all of it. Passing `/mnt/c/...` to the `.exe`, or `C:\...` to the Linux build, fails with a message naming the path form to use.

## Create is refused

Every refusal below happens before a lane directory, registration or pinned ref exists, so there is nothing to clean up. The error code is stable and is the same from the command line and from MCP.

| Code (exit) | Cause | What to do |
| --- | --- | --- |
| `not-a-repository` (3) | The directory is not inside a Git repository | Run from a checkout, or pass `--repo DIRECTORY` |
| `bare-repository` (3) | The repository has no working tree | Use a normal clone; lanes are cut from a checkout |
| `no-commits` (3) | The repository has no commit yet | Make an initial commit |
| `git-unsafe-directory` (3) | Git refuses the directory because another account owns it (common when WSL or a container opens a Windows checkout) | Run as the owner, or add the directory to Git's `safe.directory` yourself. Workcopy never edits Git configuration |
| `ref-not-found` (2) | `--branch` names nothing Git can resolve to a commit | Check the name with `git branch -a` |
| `invalid-name` (2) | A lane name must be 1 to 64 letters, digits, dots, underscores or hyphens, start with a letter or digit, and also be a portable directory name: no `con`, `prn`, `aux`, `nul`, `com1` to `com9`, `lpt1` to `lpt9`, no trailing dot | Choose another name |
| `invalid-selection` (2) | A `--project` value is not a project ID or a usable repository-relative directory (for example `.`) | Run `discover`; use `--all` on the command line to take the whole repository (MCP has no `--all`) |
| `unknown-profile` (2) | The profile is not in the configuration | Run `discover` to list profiles |
| `unportable-source-path` (3) | A selected tracked path cannot be written safely on every supported platform: reserved Windows names (`con.txt`), `: ? * < > \| "`, a trailing dot or space, `.git` or `.workcopy` as a component | Leave it out with `--project` or a profile, or rename it in the repository. Workcopy never renames a file for you |
| `case-collision` (3) | Two tracked paths differ only by case, so they would be one file on Windows or macOS | Rename one in the repository, or leave them out of the selection |
| `name-collision` (3) | The volume that would hold the lane stores two tracked paths as one entry. The usual cause is the same accented name written two ways (`café` as one code point and as `e` plus a combining accent), which macOS treats as one file and Linux and Windows as two | Leave one out with `--project` or a profile, rename one in the repository, or choose a lane root on a volume that keeps them apart. The message quotes both spellings with `\u` escapes because they look identical on screen |
| `unsupported-source-entry` (3) | A selected path is a Git submodule, a special tree entry, or a file over 64 MiB. The message says which | Leave it out with `--project` or a profile. Lanes do not materialize submodules |
| `unsafe-source-link` (3) | A tracked symbolic link is absolute, uses a backslash, or leaves the lane | Leave it out, or make the link relative and inside the repository |
| `symlink-unavailable` (3) | The selection tracks symbolic links and this account cannot create them in the lane root (Windows without Developer Mode or the "Create symbolic links" right, or a FAT or exFAT volume) | Enable Developer Mode, choose a lane root on NTFS, ext4 or APFS, or leave the linked paths out |

Workcopy asks the volume rather than guessing: before writing anything it replays the layout of the non-ASCII paths in a scratch directory on the lane volume (removed before it returns, and skipped when the volume keeps different Unicode spellings apart, which Linux and Windows do) and refuses with `name-collision` if two paths turn out to be one entry. Where the spellings are kept apart, both files are created with their own content.

## A lane is stuck in the creating phase

`list` shows the phase `creating` and every other command says `recovery-required` when a lane's creation did not finish: the tool call hit its deadline, the process was killed, or the disk filled. `workcopy recover NAME` retries the creation and normally completes it. If recover fails the same way each time, the cause is permanent (the message says why). Discard the partly created lane with `workcopy destroy NAME --force`: it removes the partial directory, the registration and the pinned ref, leaves every other lane alone, and frees the name. The `--force` is required because it deletes a directory; nothing in a lane that never finished creating was ever handed to an agent or a person, and no review worktree can exist for it. MCP has no `--force`, so the owner runs this from the command line. Use `--dry-run` first to see the plan.

## Merge says Git has no identity

`merge --commit` creates a commit, so Git must know an author and committer. A fresh machine, container or CI job often has neither. The error is `git-identity-missing` (exit 3), is raised before any object is written, and also appears on `--dry-run`; the prepared review is untouched, so approve the same receipt again after fixing it. Either set the identity once (`git config --global user.name "Your Name"` and `git config --global user.email you@example.com`) or export `GIT_AUTHOR_NAME`, `GIT_AUTHOR_EMAIL`, `GIT_COMMITTER_NAME` and `GIT_COMMITTER_EMAIL` (and optionally the `_DATE` variants) for the Workcopy process. These six are the only `GIT_*` variables Workcopy passes to Git. `GIT_DIR`, `GIT_WORK_TREE`, `GIT_INDEX_FILE` and `GIT_CONFIG_*` are always removed, so an ambient value cannot redirect a lane to another repository.

## A lane on a detached HEAD does not move

A lane created while the checkout is on a detached HEAD (or with `--branch` set to a commit) is pinned to that commit. After you integrate its review, `sync` reports exit 4 ("snapshot has not changed") because there is no branch to follow. Create a new lane from the new commit.
## A tool call hit its deadline

Large repositories on slow filesystems can need more than the default two minutes. Set `WORKCOPY_TOOL_TIMEOUT=10m` once in the [settings file](settings.md) (or start the server with `--tool-timeout 10m`). A call that was interrupted leaves a recoverable state: call `workcopy_recover`. Workcopy now overlaps file operations, which cut lane creation and review preparation to roughly a third of the previous time on Windows NTFS and WSL drvfs (about half on ext4) in the measurements recorded in [validation](validation.md); `WORKCOPY_IO_WORKERS=1` restores strictly sequential behaviour for diagnosis.

## An agent rule file is missing from a lane

Agent instruction files are copied by a catalogue of known tools plus your own list, and credentials, tool settings, media and history are always refused. Run `workcopy agent-context NAME` (MCP: `workcopy_agent_context`): it lists which agent tools the lane serves and why, known tools that have files but are not served (`notSelected`), what was copied, what was refused with the reason, and directories that look like agent context but are not recognised. If the file belongs to a tool that is not served, switch with `workcopy agent-context NAME --refresh --agent TOOL` or set `WORKCOPY_AGENTS`. If it is not in the catalogue, add the path to `WORKCOPY_AGENT_CONTEXT_INCLUDE` in the settings file. If it is a tool configuration file such as an MCP definition, the owner can opt it in with `WORKCOPY_AGENT_CONTEXT_CONFIG` (and `WORKCOPY_AGENT_CONTEXT_SECRETS` for an exact file that holds a credential). Then `workcopy agent-context NAME --refresh`. See [agent context](agent-context.md).

## A settings value is rejected

Settings are validated when a command starts, so a mistake stops it with the key or line named: an unknown `WORKCOPY_` key (usually a typo), a value out of range, a path for the wrong operating system, or a named settings file that does not exist. `workcopy settings` shows what is in effect and the file in use.

## Legacy Bash lanes

`legacy --workspace DIRECTORY` reports recognised metadata files and their hashes without changing them. Old baseline semantics may have hidden local contributions. Keep the original lane and inspect its bytes before manually importing changes into a new Go lane. There is no automatic migration.

## Reporting a defect

Use the issue template with a synthetic reproduction, exact command, exit code and `doctor` output. Include whether the failure occurred during native execution, cross-compilation or recovery. Remove private paths and content. Follow [SECURITY.md](../SECURITY.md) for vulnerabilities.

## Race-enabled validation fails before tests start

Inspect `go env GOOS GOARCH CGO_ENABLED CC`. Full validation requires cgo and, on Linux/Windows, a suitable C compiler. Windows amd64 needs the runtime libraries described in the [contributor prerequisites](../CONTRIBUTING.md#validation-prerequisites). Native Windows arm64 can run ordinary tests, but Go's race detector does not support that target. Record the limitation rather than removing the race check or treating a failed setup as a passing test run. CLI builds themselves do not require a C compiler.

## PowerShell rejects a downloaded development wrapper

Host execution policy may reject an unsigned downloaded `scripts/dev.ps1`. Run the equivalent `go run ./tools/dev setup`, `test`, `build` or `validate` task directly. Workcopy does not require changing machine execution policy; the distributed CLI and its MCP server do not use this wrapper.

## Windows Defender quarantines a freshly built workcopy.exe

Seen on 2026-11-04 and again on 2026-11-05 (signature 1.463.128.0): Defender reported `Trojan:Win32/Bearfoos.A!ml`, a machine-learning heuristic, for unsigned Go executables, and `go build`, the acceptance tests or a whole `go run ./tools/dev validate` run then failed with "the file contains a virus or potentially unwanted software" or with a wall of unrelated acceptance failures after the test binary was quarantined partway through. Evidence it is a false positive and not Workcopy code: the detection history also lists `a.out.exe` files inside Go's own build directories (compiler probe output), the v0.1.3 source built with the same flags was not flagged, the same source ran clean minutes earlier, and a stamped build (`-ldflags "-X workcopy/internal/cli.Version=..."`, as release builds are) was flagged on the second day even though it was not on the first, so stamping is not a dependable workaround. Workcopy does not obfuscate its code to avoid it. If a gate run fails with many unrelated acceptance tests, check `Get-MpThreatDetection` for a recent detection of a `wc-cli-*\workcopy.exe` before debugging the tests, restore or allow the file, and rerun. Submit the file at the Microsoft Security Intelligence portal as a false positive; do not add an exclusion on a machine you do not own. Check a release candidate with Defender on a Windows machine before publishing it, and sign Windows releases when a certificate is available.

## Test temporary directory is inside a Git checkout

The canonical `go run ./tools/dev test` and `go run ./tools/dev validate` tasks detect this condition and relocate child test processes to a local directory outside Git checkouts. If no safe local candidate is available, set `WORKCOPY_TEST_TEMP` to a private local directory and rerun. Do not place a production workspace root inside a checkout; the workspace-boundary rejection is an intentional safety control.

