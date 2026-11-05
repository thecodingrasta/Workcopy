# Validation status

This page records local evidence and the checks contributors can repeat. The CI badge links to actual workflow results; it does not certify a release before a successful run.

## Local evidence

| Check | Evidence |
| --- | --- |
| Windows amd64 / local NTFS | Native CLI, MCP, real-Git fixtures, dirty-checkout preservation, reconciliation, recovery and guarded cleanup tested |
| Three concurrent lanes | Offline Go validation, deliberate conflict, explicit resolution and separate unstaged reviews tested |
| Multiple repositories through one MCP | Three same-named lanes, per-call root override, current-directory bootstrap, concurrent offline Go tests and separate review receipts tested |
| Windows checkout identity | Distinct Unicode checkout paths retain separate configuration and default storage; regression reproduced before correction |
| Go, Node and .NET | Small selected-project fixtures built and tested in lanes; this does not certify arbitrary repositories |
| Linux WSL / Debian, ext4 | Full suite with `-race` passed natively with Go 1.27.3 and Git 2.47.3 (lanes and repositories on the WSL filesystem), including the WSL side of the interop tests and both lane fixtures. The .NET fixture ran with SDK 10.0.402 plus the .NET 8 runtime it targets; the Node fixture ran with Node 22.23 (the version CI uses) and Node 24.22, both installed under the user's home directory with no system changes (see below) |
| Linux WSL / Debian, `/mnt/c` drvfs without metadata | The failing environment of WC-001/WC-004: every new file reports mode 777. v0.1.3 shows phantom modifications on almost every lane test; the current code passes the workspace, state, file-system, line-ending and Git suites with `-race` with repository and lanes both on that mount. Only the Debian instance was exercised, not Windows-side Git interop |
| macOS | Cloud CI is the only evidence: `macos-latest` (Apple silicon) and `macos-15-intel` run the full gate on every push. Nothing in this pass was executed on a Mac, so macOS behaviour of the new line-ending, file-mode and parallel-I/O code is unconfirmed until those jobs have run |
| Other architectures | Cross-compilation only unless a native runner explicitly executes the checks |
| Case-only filename collision | Tested on every platform by writing the two paths straight into Git's object store, so the case-insensitive volume does not need to hold them; the refusal is exit 3 `case-collision` naming both paths |

The clean-source Windows gate passed with Go 1.25.6, Git 2.45.1.windows.1 and MinGW-w64 GCC 15.2.0 with `libsynchronization.a` available. It included formatting, vet, race tests, the offline Node lane fixture, a native build and JSON CLI smoke checks. The selected .NET lane restore/build/test passed with SDK 10.0.401 and .NET 8 runtime 8.0.31. The repository now pins Go 1.27.3; Windows and Debian WSL scans with `govulncheck` v1.9.0 report no vulnerabilities under that toolchain. Older Go 1.27.2 installations must allow automatic toolchain selection or be upgraded before release packaging. CI's `1.25.x` job is an API-floor check, not a release-toolchain approval. A local pass does not imply a newly edited workflow has run on GitHub.

Repeat the native gate with both lane fixtures enabled:

```powershell
$env:WORKCOPY_DOTNET_LANE_ACCEPTANCE = '1'
$env:WORKCOPY_NODE_LANE_ACCEPTANCE = '1'
go run ./tools/dev validate
```

The .NET fixture restores pinned test packages into disposable storage. Obtain approval for network access before running it locally. Node runs tiny offline fixture scripts without installation. GitHub CI enables these fixtures explicitly.

## Repository shapes, refusals and setups

`tests/acceptance/repository_shapes_test.go` and `repository_refusals_test.go` drive the shipped executable (and, for the refusals, an MCP session) against the repository and filesystem shapes users really have. One shared lifecycle proves several properties per shape: the lane mirrors the pinned commit byte for byte (object IDs, symbolic links and, where the filesystem is honest, execute bits); `status` reports exactly the three edits made; the review commit contains exactly those three changes on top of the pinned commit; the main checkout, its status and an untracked human file are unchanged; the commit integrates with `git cherry-pick`; and `destroy` leaves no lane, registration or pinned ref.

| Shape | What it proves |
| --- | --- |
| Unicode, spaces and punctuation in names | Names with `日本語`, accents, `;`, `&`, `#`, `%`, a leading `-`, an apostrophe and an emoji survive create, review and commit |
| Empty, binary, 3 MiB, no-newline, BOM, CRLF, lone CR and mixed-ending files | Bytes are never altered when no attribute asks for conversion |
| Detached HEAD, shallow clone, linked worktree, sparse checkout | Lanes are cut from the commit, not the checkout; a shallow clone and a linked worktree work with the commit objects they share |
| Started from a subdirectory; ambient `GIT_DIR`, `GIT_WORK_TREE` and `GIT_INDEX_FILE` naming another repository | The lane comes from the right repository whatever the environment says |
| Repository path with spaces and Unicode and the default lane root | Default storage and path handling |
| 1,200 files in nested directories; a path longer than Windows `MAX_PATH` | Scale and long-path handling |
| Executable files and relative symbolic links (where the account can create links) | Mode and link preservation through a full lifecycle |

The refusal tests check the exit code, the stable error code, that the message names the path and the way out, that nothing was created, and that the advertised workaround works: no commits, bare repository, not a repository, unknown branch; submodules, files over 64 MiB, 17 kinds of unportable path (reserved Windows names, `:`, `?`, `*`, `<`, `|`, trailing dot or space, `.git` and `.workcopy` components, backslashes), case-only collisions, eight kinds of unsafe symbolic link, canonically equivalent Unicode names, lane names that try to escape the root or are reserved, case-variant lane names, symbolic links on an account that cannot create them, and a missing Git identity (including that the same receipt commits once an identity is supplied through the environment). The same codes are checked over MCP.

Each fix was also checked by removing it and confirming the matching test fails.

| Environment | Result |
| --- | --- |
| Windows 11, NTFS, Git 2.56 | Full gate with `-race`, both lane fixtures, and all of the above passed |
| WSL Debian ext4, Git 2.47 | Full `-race` suite with both lane fixtures passed in about one minute |
| WSL Debian, repository and lanes on `/mnt/c` without metadata (every file reports mode 777) | Engine packages and the whole acceptance package passed; slow, as documented in [WSL](wsl.md) |
| WSL Debian, strictly sequential I/O (`WORKCOPY_IO_WORKERS=1`) | Passed |
| Windows Git against Linux Git, both directions (`tests/interop`) | Passed |
| macOS (Apple silicon and Intel) | Not run locally. The same tests run in the CI gate; the canonically-equivalent-Unicode test is the one most likely to behave differently there, because APFS treats `é` written two ways as one file |

### Running the lane fixtures natively on Linux

CI installs the toolchains with `actions/setup-node` and `actions/setup-dotnet`. To repeat it on a Linux or WSL machine without touching the system packages, unpack them under your home directory:

```bash
mkdir -p ~/tools && cd ~/tools
# .NET SDK plus the 8.0 runtime the fixture targets (needs the libicu package your distribution ships)
curl -fsSL https://dot.net/v1/dotnet-install.sh -o dotnet-install.sh
bash dotnet-install.sh --version 10.0.402 --install-dir ~/tools/dotnet --no-path
bash dotnet-install.sh --runtime dotnet --channel 8.0 --install-dir ~/tools/dotnet --no-path
# Node 22 from https://nodejs.org/dist/ (linux-x64 tarball), unpacked to ~/tools/node22
export PATH=$HOME/tools/node22/bin:$HOME/tools/dotnet:$PATH DOTNET_ROOT=$HOME/tools/dotnet
WORKCOPY_DOTNET_LANE_ACCEPTANCE=1 WORKCOPY_NODE_LANE_ACCEPTANCE=1 go test ./tests/acceptance -run 'TestSelectedDotNet|TestSelectedNode' -count=1 -v
```

The .NET fixture restores pinned packages from NuGet, so it needs network access. On Debian the SDK fails to start without ICU (`Couldn''t find a valid ICU package`); install `libicu-dev` rather than switching globalization off, so the test matches a real setup. The fixture targets `net8.0`: with only the 10.0 runtime installed, `dotnet test` fails with "You must install or update .NET to run this application", which is a missing runtime, not a Workcopy fault.

If the `TEMP` directory of the machine running the tests lies inside a Git checkout, every lane root is refused as `workspace-inside-repository`. That is Workcopy's safety rule working; point `TEMP` or `TMPDIR` at a directory outside any repository.
## Measured performance

`internal/workspace/lifecycle_bench_test.go` times create, status and review on a synthetic repository (`WORKCOPY_BENCH_FILES=4000 go test ./internal/workspace -run '^$' -bench LaneLifecycle -benchtime=1x`, with `TEMP`/`TMPDIR` pointing at the filesystem under test). Files are about 15 KB. All figures are single runs on one development machine with real-time antivirus on Windows. They show direction, not a benchmark suite.

| Filesystem and size | Create | Status | Review |
| --- | --- | --- | --- |
| Windows NTFS, 4,000 files, v0.1.3 | 65.9 s | 32.7 s | 50.5 s |
| Windows NTFS, 4,000 files, now | 7.3 s | 6.6 s | 14.4 s |
| Windows NTFS, 7,500 files, now | 12.9 s | 12.3 s | 26.7 s |
| WSL ext4, 7,500 files, now | 3.1 s | 0.25 s | 4.0 s |
| WSL `/mnt/c` (drvfs), 7,500 files, now | 125 s | 8.2 s | 150 s |

Three changes produced this. File operations overlap (a bounded worker pool). A new lane writes each file straight from Git instead of first staging a second copy in the lane's object store and deleting it afterwards. Review preparation avoids re-reading files it has just verified. The per-file `fsync` WC-003 suspected is not the main cost (removing it saved about 5% on NTFS), so durability was kept.

The Linux build on `/mnt/c` stays 10 to 20 times slower than the Windows build on the same drive when repository and lanes are both there. Raising the worker count from 4 to 32 changed nothing. The next section says why, and what does fix it.

## Why `/mnt/c` is slow (researched)

Measured on one machine (WSL 2.7.13, kernel 6.18.33, Debian, Windows 11, real-time antivirus on), 2026-11-04. `/mnt/c` is a 9p mount served by Windows (`aname=drvfs`, `msize=65536`, `cache=0x5`). Microsoft's [file-system guidance](https://learn.microsoft.com/en-us/windows/wsl/filesystems) says the same in general terms: for the fastest performance, store files in the WSL file system when working from Linux.

Cost of one filesystem call from a Go program, single thread, in milliseconds (500 files in one directory):

| Call | ext4 | `/mnt/c` (9p) | Windows build on NTFS |
| --- | --- | --- | --- |
| `lstat` | 0.001 | 1.28 | 0.035 |
| create + write 4 KiB | 0.007 | 3.13 | 0.35 |
| `rename` | 0.005 | 4.16 | 0.52 |
| `chmod` | 0.001 | 1.36 | 0.07 |
| `remove` | 0.003 | 1.61 | 0.11 |

- **Every call is a round trip to Windows**, and each path component is another walk: an `lstat` at depth 8 costs 2.1 ms against 0.4 ms at depth 1. Workcopy confines every path to the lane root, which costs more again (3.6 ms at depth 8; checking every parent directory costs 12.8 ms).
- **Reads overlap, writes mostly do not.** With four workers `lstat` falls to 0.15 ms and a read to 0.33 ms, but create (1.4 ms) and `rename` (1.8 ms) only halve: the Windows side serialises metadata changes. That is why more workers did not help.
- **Mount options are not a fix.** On extra mounts of `C:\`, `msize=262144` changed create from 1.45 to 1.24 ms and `lstat` from 0.13 to 0.11 ms; `cache=loose` halved `lstat` (0.13 to 0.06 ms) and left create and `rename` unchanged; the `metadata` option made no measurable difference. `cache=loose` also stops Linux noticing changes made from Windows, which a tool that coordinates people and agents should not accept.
- **Inside Workcopy**, creating 1,000 files with repository and lane on `/mnt/c` spent per file: 28 `lstat` calls, one `rename` (5.7 ms), one create (2.7 ms), a wasted `remove` of the temporary file after the rename (2.3 ms), `close` 1.3 ms, `fsync` 1.0 ms. This release removed the wasted `remove` and the duplicate `lstat` checks (28 to 17 `lstat` calls per file). The wall-clock gain was a few percent: the work that remains is the create, `rename` and `close` that atomic replacement needs. A directory-trust cache that would skip re-checking parent directories was built, measured at about 6% on 9p, and not kept, because that gain did not justify weakening the per-operation parent check.
- **Loose Git objects make it worse.** The synthetic repository stores one file per object; creating 1,000 files took 15.9 s with them and 6.0 s after `git gc`. Real repositories are mostly packed.
- **Not tested:** the effect of a Windows Defender exclusion for the repository (excluding a path needs administrator rights, which this session did not have).

`go run ./tools/fsbench DIRECTORY` repeats the per-call measurements on any directory, and `WORKCOPY_BENCH_LANE_ROOT` places the lanes of the lifecycle benchmark on another filesystem than the repository.

What fixes it is moving the lane I/O off the mount. With the repository still on `/mnt/c` and lane storage on ext4 (`WORKCOPY_ROOT`), a packed 1,000-file repository:

| | Create | Status | Review |
| --- | --- | --- | --- |
| Repository and lanes on `/mnt/c` | 6.4 s | 1.1 s | 8.8 s |
| Repository on `/mnt/c`, lanes on ext4 | 0.46 s | 0.05 s | 0.75 s |

The Windows `workcopy.exe` remains the right tool when Windows programs must open the lanes.

## Interoperability and test coverage

- **Windows Git versus Linux Git** (`tests/interop`, needs a machine with WSL). Both directions pass here: the Linux build and Linux Git driving a repository on a Windows drive next to the Windows build, and `workcopy.exe` driven from inside WSL with Windows paths. They check that both builds write identical bytes for attribute-fixed files, that a review made by Linux Git is reported `prunable` by Windows Git (the documented limitation, reproduced) and that `doctor` warns about it, that a review made by `workcopy.exe` is healthy for Windows Git, `WORKCOPY_GIT`, and the foreign-path messages. Hosted CI runners have no WSL, so these skip there.
- **Agent selection and configuration opt-in** are tested at four levels: the client-name table and selection precedence (`internal/agents`), the planner with negative cases for every refusal tier (`internal/agentctx`), lanes built by the engine for each tool including switching, legacy lanes and the destroy/review/commit contract (`internal/workspace`), real MCP sessions that announce `claude-code`, `codex-mcp-client`, `gemini-cli-mcp-client`, `Kiro CLI`, `opencode` and an unknown client (`internal/mcpserver`), and the real binary with the settings file and `--agent` (`tests/acceptance`). Each guard was also checked by breaking the code it protects and confirming a test fails. MCP tool schemas are checked to offer no argument that would let an agent widen what is copied.
- **Every command and tool is exercised.** The acceptance package reads the command list from the binary's own help and fails if any command was not run through the real executable. The MCP package asks the server for its tool list and fails if any tool was not called. Both checks run on every full test run on every platform.
- **.NET and Node** lane fixtures (real NuGet restore, .NET SDK 10.0.402; Node 24.19) pass with the opt-in variables set; CI runs them on Linux, Windows and both macOS architectures.
- **Properties:** the line-ending converter is checked byte for byte against `git checkout` (2,720 cases) and by a round-trip fuzz target (4.3 million executions without failure); the settings parser has a fuzz target; the worker pool is checked sequentially and in parallel; six concurrent processes share one root and repository without corruption.
## CI gates

`.github/workflows/ci.yml` runs:

- Go 1.25 API floor and current stable Go on Linux (x86-64 and arm64), Windows (x86-64) and macOS (Apple silicon and x86-64): formatting, vet, race tests, real CLI/MCP journeys, build and diagnostics.
- A Linux job that loop-mounts a FAT volume reporting every file as executable (the WSL drvfs symptom, checked by the job itself before it relies on it) and runs the lane, review, commit and cleanup test on it, then runs the state and workspace suites with `WORKCOPY_IO_WORKERS=1` to show the worker pool changes no result. Hosted runners have no WSL, so path-direction and drive-warning logic is covered by unit tests instead.
- Explicit selected .NET restore/build/test and offline Node script fixtures on all three operating systems.
- Current vulnerability database scanning. Scanner/network errors fail the job.
- Native candidate bare executables, checksums and matching source, uploaded as temporary artifacts after native checks.

Fixture restores in CI download only the fixture's pinned packages. Workcopy itself never restores packages or executes arbitrary build scripts. CI runner architectures can change; use each run's `go env`/runner evidence rather than infer architecture from an OS label.

## Release qualification

The manual draft-release workflow validates the reviewed tag, builds six OS/architecture combinations, smoke-tests the native runner assets and attaches checksums plus dependency-inclusive source. Targets without native execution remain compile-only. A maintainer reviews results and publishes the draft.

No laptop check can prove Linux/macOS runtime behaviour, future GitHub availability or every consumer project's build inputs. Contributions with a minimal disposable fixture and exact platform/tool versions are welcome. Follow [CONTRIBUTING.md](../CONTRIBUTING.md) and [release procedure](releasing.md).

The optional Node acceptance locates a native `node` executable paired with a real `npm-cli.js` on `PATH` without invoking shell wrappers. Its test assertion checks the stable `tests 1`/`pass 1` summary across supported Node output formats rather than a version-specific `# pass 1` spelling. The canonical task runner also relocates child tests away from a temporary directory inside a Git checkout when needed.

