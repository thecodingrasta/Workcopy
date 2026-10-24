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

## Legacy Bash lanes

`legacy --workspace DIRECTORY` reports recognised metadata files and their hashes without changing them. Old baseline semantics may have hidden local contributions. Keep the original lane and inspect its bytes before manually importing changes into a new Go lane. There is no automatic migration.

## Reporting a defect

Use the issue template with a synthetic reproduction, exact command, exit code and `doctor` output. Include whether the failure occurred during native execution, cross-compilation or recovery. Remove private paths and content. Follow [SECURITY.md](../SECURITY.md) for vulnerabilities.

## Race-enabled validation fails before tests start

Inspect `go env GOOS GOARCH CGO_ENABLED CC`. Full validation requires cgo and, on Linux/Windows, a suitable C compiler. Windows amd64 needs the runtime libraries described in the [contributor prerequisites](../CONTRIBUTING.md#validation-prerequisites). Native Windows arm64 can run ordinary tests, but Go's race detector does not support that target. Record the limitation rather than removing the race check or treating a failed setup as a passing test run. CLI builds themselves do not require a C compiler.

## PowerShell rejects a downloaded development wrapper

Host execution policy may reject an unsigned downloaded `scripts/dev.ps1`. Run the equivalent `go run ./tools/dev setup`, `test`, `build` or `validate` task directly. Workcopy does not require changing machine execution policy; the distributed CLI and its MCP server do not use this wrapper.

## Test temporary directory is inside a Git checkout

The canonical `go run ./tools/dev test` and `go run ./tools/dev validate` tasks detect this condition and relocate child test processes to a local directory outside Git checkouts. If no safe local candidate is available, set `WORKCOPY_TEST_TEMP` to a private local directory and rerun. Do not place a production workspace root inside a checkout; the workspace-boundary rejection is an intentional safety control.

