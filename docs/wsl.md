
# Windows, WSL and `/mnt/c`

Workcopy runs natively on Windows, Linux and macOS. Under WSL there are two ways to reach a Windows checkout, and they behave very differently. This page says which to use, what the slow path costs, and why.

**Short version.** Keep the repository and the lanes on the Linux filesystem (`~/src`) if you can. If the repository must stay on a Windows drive, either run the Windows `workcopy.exe`, or run the Linux build with `WORKCOPY_ROOT` pointing at a Linux directory. Do not put both the repository and the lanes on `/mnt/c` with the Linux build unless you accept the slowness below.

## Which setup should I use?

| Repository | Lanes | Program | Speed | Windows tools see the lanes | Notes |
| --- | --- | --- | --- | --- | --- |
| WSL filesystem (`~/src`) | beside it | Linux `workcopy` | fastest | no (only through `\\wsl$`) | Recommended when the agent runs in WSL. Nothing is shared with Windows Git. |
| Windows drive | Windows drive | Windows `workcopy.exe`, called with Windows or `/mnt/c` paths | fast (native NTFS) | yes | Recommended when Visual Studio or other Windows tools must open the lanes. |
| Windows drive (`/mnt/c`) | Linux directory (`WORKCOPY_ROOT`) | Linux `workcopy` | fast | no | Good compromise. The review worktree is still registered in the Windows checkout (see below). |
| Windows drive (`/mnt/c`) | `/mnt/c` | Linux `workcopy` | **10 to 20 times slower** | yes | Works, but only for small repositories or patience. `workcopy doctor` and `workcopy create` warn about it. |

## Why `/mnt/c` is slow

`/mnt/c` is not a disk. It is a 9p network file system served by Windows, and every file operation is a round trip to the Windows side. A Linux program that makes many small calls pays that price on each one. Workcopy writes every file atomically (create, write, flush, rename) and checks every parent directory, so lane creation is made of exactly the calls that are most expensive there.

One machine, one thread, milliseconds per call ([full table and method](validation.md#why-mntc-is-slow-researched)):

| Call | Linux ext4 | `/mnt/c` | Windows build on NTFS |
| --- | --- | --- | --- |
| `lstat` | 0.001 | 1.28 | 0.035 |
| create and write 4 KiB | 0.007 | 3.13 | 0.35 |
| `rename` | 0.005 | 4.16 | 0.52 |

What this means for a 7,500-file repository with the Linux build and everything on `/mnt/c`: creating a lane took about 125 s and preparing a review about 150 s. With the repository and the lanes both on ext4 the same repository took 3 s and 4 s, and 13 s and 27 s with the Windows build on NTFS.

What does not help, and was measured:

- **More worker threads.** Reads overlap across workers, but Windows serialises the create and rename calls, so going from 4 to 32 workers changed nothing.
- **Mount options.** A larger `msize` and `cache=loose` each moved the numbers by under 15%. `cache=loose` also stops Linux noticing edits made from Windows, which is the wrong trade for a tool that coordinates people and agents. Do not use it.
- **Dropping `fsync`.** It was about 5% of the cost, so Workcopy keeps it.

A Windows Defender exclusion for the repository might help. It was not tested, because excluding a path needs administrator rights.

## Drawbacks when the repository is on a Windows drive

These apply whenever the Linux build works on a checkout that Windows Git also uses.

- **Review worktrees look broken to Windows Git.** The Linux build registers the review worktree with Linux paths, so `git worktree list` run by Windows Git marks it `prunable`, and a Windows `git gc` or `git worktree prune` can drop its metadata. The commit objects are safe; the worktree registration is not. The review made by `workcopy.exe` is healthy for Windows Git. `workcopy doctor` warns about this combination.
- **A symlink made by `share` is unreadable from Windows.** Windows programs cannot follow a Linux symlink that targets a Linux path.
- **File modes can lie.** Without the `metadata` mount option, `/mnt/c` reports every file as executable. Workcopy detects this when a lane is created, ignores the false execute bits and repairs lanes made by v0.1.3. It cannot know a script's real mode on such a mount.
- **Path spelling is handled for you.** See [path forms](#path-forms).
- **Line endings come from the Git that runs.** Under WSL the Linux Git's `core.autocrlf` and `core.eol` apply, not Windows Git's. `workcopy doctor` prints what is in effect.

## Path forms

A Windows checkout has two names under WSL: `C:\work\repo` from Windows and `/mnt/c/work/repo` from Linux. Workcopy accepts both, in every flag, every setting and every MCP argument, so an agent in WSL can drive `workcopy.exe` with the `/mnt/c/...` paths it already has and no `wslpath` wrapper.

| Build | Given | Result |
| --- | --- | --- |
| Windows `workcopy.exe` | `C:\work\repo` or `c:/work/repo` | used as is |
| Windows `workcopy.exe` | `/mnt/c/work/repo` | read as `C:\work\repo` |
| Windows `workcopy.exe` | `/home/me/repo` | refused, `wsl-path-unreachable`: Windows cannot open WSL's own filesystem. Use the Linux build for a repository there |
| Linux `workcopy` in WSL | `C:\work\repo` or `C:/work/repo` | read as the drive's mount point (`/mnt/c/work/repo`, found from the kernel's mount table, so a moved `automount root` works) |
| Linux `workcopy` in WSL | `\\wsl.localhost\<this distro>\home\me` | read as `/home/me` |
| Linux or macOS outside WSL | `C:\work\repo` | refused, `windows-path-on-posix`: there is no such drive |
| any | another distribution's or machine's share | refused with the reason |

**Answers use the spelling you used.** A call that passed `/mnt/c/...` is answered with `/mnt/c/...` in lane paths, review paths, warnings and suggested commands; a call that passed `C:\...` is answered with `C:\...`. The choice belongs to the call, so two agents using different spellings on one server each read their own. Conflict content is data and is never rewritten. To force one spelling for everything, set `WORKCOPY_PATH_STYLE` (or `--path-style`) to `wsl`, `windows` or `native`. The Windows build assumes WSL's default mount root (`/mnt`); set `WORKCOPY_WSL_MOUNT_ROOT` if `/etc/wsl.conf` changed it. It can only know that one root: a second mount of a drive somewhere else (for example `/mnt/cnm`) is not a drive path to it, and is refused as `wsl-path-unreachable`. The Linux build reads the kernel's mount table and handles any mount of a drive.

`workcopy path NAME` prints the lane directory alone, in the spelling you used, so a script on either side can `cd` into it.

## Setting it up

Set the lane location once in the [settings file](settings.md); every command and MCP server then uses it:

```bash
workcopy settings --init            # writes a commented template, never overwrites
# then in ~/.config/workcopy/workcopy.env:
WORKCOPY_ROOT=/home/you/.workcopy/lanes
```

Check the result:

```bash
workcopy doctor      # reports the Git program, line endings, file mode, and warns about a Windows drive
workcopy settings    # shows every value and where it came from
```

To measure your own machine, `go run ./tools/fsbench DIRECTORY` repeats the per-call timings on any directory.

## Large repositories

Even on a fast filesystem a very large repository can exceed the two-minute MCP deadline. Raise it once with `WORKCOPY_TOOL_TIMEOUT=10m` (up to 24 hours) in the settings file. A call that is cut off leaves a recoverable state: run `workcopy recover NAME`, or call `workcopy_recover`.

See also [troubleshooting](troubleshooting.md#wsl-windows-repositories-slow-operations-worktrees-visual-studio-cannot-open) and [validation](validation.md) for the interoperability tests that cover Windows Git against Linux Git in both directions.
