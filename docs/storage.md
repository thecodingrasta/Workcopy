# Dependency storage

Editable source is independent per lane. Workcopy never hardlinks it to the original checkout. Successful creation removes transient content blobs used by its transaction. Accepted committed content is referenced through Git object IDs and pinned refs, not a second permanent source copy in every lane.

Conflicts and interrupted operations can temporarily retain content under `.workcopy/objects`. Recovery and successful transactions retain only objects still needed by pending records. Do not manually clear that directory.

## What uses storage across lanes

| Content | Default behavior | Keep storage predictable |
| --- | --- | --- |
| Selected source | Each lane has its own editable copy. Unselected projects are omitted. | Select only the projects and shared inputs the task needs. Source copy-on-write savings are not guaranteed. |
| Package downloads | Many package managers keep a per-user cache outside the lane, so same-user lanes can reuse it automatically. | Use the package manager's concurrency rules. Workcopy does not install, configure or validate caches. |
| Build and test outputs | `bin`, `obj`, generated files, coverage and intermediates stay with each lane unless the build is explicitly configured otherwise. These can duplicate across lanes. | Keep outputs private to each lane. If relocating them, use a unique output directory per lane. Workcopy does not pool or cap them. |
| Human review | Each prepared review materializes full repository source plus the contribution. | Include each retained review in the disk budget and prepare reviews sequentially. |

The lane-local output rule keeps independent builds from replacing one another's files. It does not prevent a build script from writing outside the lane; inspect custom output paths and use host permissions when those writes must be restricted.

## Measuring a lane

```text
workcopy storage NAME            # also: workcopy_storage over MCP, and --json
```

The command is read-only. It walks the lane without entering any link, asks the operating system for each file's real allocation, and counts a file with several names once. It reports:

| Row | Meaning |
| --- | --- |
| `source` | Files the lane tracks. A tracked file stays `source` even inside a directory named `bin`. |
| `workcopy-metadata` | The lane's own state under `.workcopy`. |
| `agent-context` | Copies of agent instruction files. |
| `declared-output` | Untracked files matching the owner's `destroy.discard` patterns. Only an owner-supplied configuration counts; a repository-committed one is ignored. |
| `likely-output` | Untracked files under conventional output directories (`bin`, `obj`, `node_modules`, `dist`, `build`, `out`, `target`, `artifacts`, `TestResults`, `coverage`, `.next`, `__pycache__`), or under a centralised .NET output directory that a tracked `Directory.Build.props` already declares with `UseArtifactsOutput` or an `ArtifactsPath` of the form `$(MSBuildThisFileDirectory)name`. This is a naming heuristic, not proof the content is disposable. |
| `other-local` | Everything else the lane holds. |

Each row shows allocated bytes, how much of that is `reclaimable` (every name of the file lies inside the lane, so deleting the lane frees it) and how much is `shared` (a name outside the lane also reaches it, for example a hard link into a package store, so deleting the lane frees nothing). The report also names the largest top-level directories, so a full disk can be traced to a place.

What it does **not** do:

- It is not a quota and does not enforce anything. Child build tools write wherever they are told to, including outside the lane.
- Shared dependency mappings and other links are listed as "not entered". Their targets belong to every lane that links them and are never added to a lane's total.
- Data shared by block cloning cannot be seen through the operating system's file APIs, so on a volume that clones, the allocation figure is an upper bound. The report says so.
- Files that cannot be read are counted as `unreadable`; a nonzero count means the totals are a lower bound.
- The figures are a point-in-time estimate. No lock is taken, so a running build changes them while they are read.

`workcopy doctor` and `storage` also report the volume under the repository and lane root: filesystem, whether hard links work, whether the volume reports block sharing, and free space. Block cloning is read from the volume flag Windows documents for it (`FILE_SUPPORTS_BLOCK_REFCOUNTING`), not from the filesystem name. On Linux and macOS the answer comes from the filesystem type: `unknown` means only an attempt on the real volume could decide (for example XFS, which shares blocks only when created with reflink enabled), and is not a refusal. A reported capability is not proof that a copy of a particular file will succeed.

The report also lists the per-user NuGet and Go caches (taken from `NUGET_PACKAGES`, `GOCACHE`, `GOMODCACHE`, `GOPATH`, or the documented default; `NuGet.Config` and `go env -w` settings are not read) and warns when one is on a different volume than the lanes. That matters because link-based reuse between volumes cannot work and, in the measurements below, fell back to copying without any message.

## Measured behaviour

These numbers come from experiments on one Windows 11 machine (NTFS, .NET SDK 10.0.402, Go 1.27.3, pnpm 12.18.0). They are evidence about what is possible, not a promise about any other machine, and they were not repeated on macOS.

- **Package caches already share.** Go's build and module caches and NuGet's global packages are per user, so lanes run by the same user reuse them without any Workcopy link. A separate Go build cache per lane roughly doubled cache size in a two-lane build (140.2 MiB against 74.7 MiB shared), and both modes produced the same binary for the same lane.
- **Output is small in the .NET project measured and large in `node_modules`.** A clean build of two small .NET projects added about 1.5 MiB per lane; two independent copies of an npm `node_modules` tree (32,525 files, 584.5 MiB logical) allocated 1,265.3 MiB.
- **A shared pnpm store helps when packages are plain files.** With the store on the same volume as the lanes, two lanes of that project allocated 615.8 MiB in total against 1,265.3 MiB for two `npm ci` installs, and the second install took 9 s against 34 s. On a second, native-module-heavy project the saving was only 4% (1,157.5 MiB against 1,209.2 MiB), because about 11,000 files in packages that declare install scripts were copied, not linked, even with scripts disabled.
- **Linking is not isolation.** An in-place write to a hard-linked file changed the same file in every lane and in the store. `csc` and MSBuild's `Copy` task replaced files instead of writing into them, so a rebuilt lane broke its link and left the other lane untouched. A read-only attribute made the build fail with `MSB3491` instead of corrupting anything, but the attribute applies to every link.
- **MSBuild copy-local hard links** (`CreateHardLinksForCopyLocalIfPossible`) linked 88 of 99 output files, 86 of them into the global NuGet cache, so an in-place write would reach every project that uses the package. With the lane and the NuGet cache on different volumes only 2 of 99 files were linked, while the build log still printed 88 "Creating hard link" lines. The log does not prove sharing; only the file identity does.
- **Identical source does not give identical output by default.** The same project built in two lane directories produced different DLLs and PDBs. With an explicit `PathMap` for the lane root and `ContinuousIntegrationBuild=true`, the `bin` output became byte-identical, but intermediates such as `assets.cache` and `FileListAbsolute.txt` still differed because they embed absolute paths. A shared build-output store would therefore need identical path maps in every lane, and `PathMap` changes the source paths recorded in debug symbols.

## What Workcopy will not do to your build

Workcopy never edits `Directory.Build.props`, `.npmrc`, `pnpm-workspace.yaml` or any other build file, never passes properties to a build tool, and never creates hard links or clones inside a lane on its own. The measurements above show each of those changes what is built or widens what a stray write can reach. Where a repository already opts in to a layout (a centralised .NET output directory), `storage` reads it and classifies output accordingly. Anything that would change build behaviour must be an explicit, recorded, per-lane choice made by the owner, and none exists today.

## Prefer native package stores

A package manager often keeps downloads outside the lane, so lanes running as the same user may already reuse one cache without a Workcopy link. Workcopy does not configure package managers, run restores or assume that an installed dependency tree is immutable.

A cache may be shared across lanes only when that package manager documents safe concurrent use and the lanes use compatible runtimes and lockfiles. For example, Go documents concurrent access to its module cache and build cache. NuGet coordinates concurrent cache access through its shared `NuGetScratch` directory; its documentation warns that it does not lock concurrent restores of the same project. Follow the requirements for the package-manager version in use. Workcopy does not verify cache safety or coordinate writes to a shared target. If the concurrency contract is unknown, keep the cache separate per lane. See the [Go module cache](https://go.dev/ref/mod#module-cache), [Go build cache](https://pkg.go.dev/cmd/go#hdr-Build_and_test_caching) and [NuGet cache guidance](https://learn.microsoft.com/nuget/consume-packages/managing-the-global-packages-and-cache-folders).

Build outputs, compiler intermediates and virtual environments can depend on absolute paths, runtime versions or project settings. Keep them lane-local unless their sharing contract has been independently checked. A directory name is not proof that its contents are safe to share.

Keep mutable outputs such as `bin`, `obj`, generated source, coverage data and temporary build products private to each lane. These outputs can be duplicated across lanes and are not included in Workcopy's source-allocation estimate or capped by its reserve. They still reduce free space, which can block later Workcopy operations. Give each lane a storage budget and use the build tool's clean command only for outputs confirmed disposable. If a build tool supports relocating outputs, configure a unique output directory per lane. Never point concurrent lanes at one writable output directory: source versions and build settings can differ, and one build can replace another lane's artifacts. Workcopy does not share or deduplicate build outputs automatically.

## Fields added after v0.1.3

Lane manifests may carry `eol` on a file version (`lf` or `crlf`, meaning the file is text, is compared in its LF form and is written in that form) and `ownerConfig` (the configuration came from an owner-supplied file). Registrations may carry `branch`. Manifests may carry `contextAgents` (the agent tools the lane serves: tool keys, `all` or `generic`) and each `agentContext` record may carry `sensitive` (the owner named that exact file as possibly holding credentials). All are optional and absent means the v0.1.3 behaviour, so existing lanes keep working; a v0.1.3 binary cannot read a manifest or registration that contains them.

## Explicit sharing

The command form is:

```text
workcopy share NAME --path packages --target DIRECTORY --dependency-identity IDENTITY --trust-shared
```

`DIRECTORY` must be an existing real local directory, outside the workspace root and not an ancestor exposing the repository or lanes. `packages` is the destination relative to the lane. It must not already exist, intersect tracked source or overlap another mapping.

`IDENTITY` is your description of the compatible dependency environment, such as the package-manager/runtime version and lockfile identity. It is not a secret or a verified compatibility certificate.

Workcopy does not determine whether the destination is a package cache or a build output. Keeping build outputs private is an operator rule, not a command guard. The recorded identity and manifest fingerprints do not make a shared target safe for concurrent writes.

Workcopy records the mapping, the platform and hashes of recognised dependency manifests/lockfiles. `status` warns about input drift or a missing/retargeted mapping. It does not hash gigabytes of installed packages or prove that two lockfiles imply identical tool behaviour.

The link is a symlink on Linux/macOS and a junction on Windows. A tracked source symbolic link is stored in Git's form (`bin/run.sh`); Windows resolves a relative link only with backslashes, so the target it reports is read back with `/` for comparison, and an absolute mapping target is compared exactly as Windows reports it. Windows targets must use a local drive, not a UNC share. Failure is explicit; there is no marker-file fallback and no automatic large copy. Each call links one lane-relative path to one existing directory. Linking the same target from several lanes gives all of them writable access to the same files; it does not combine separate build outputs or make a cache safe. Use it only for a dependency directory whose tool-specific concurrency and compatibility rules you have checked. Never use it to pool `bin`, `obj` or other mutable build outputs.

Shared links are writable and Workcopy does not lock operations inside their targets. A restore, package script or agent can change the shared target and affect every lane using it. Do not run mutating operations against it without coordinating all consumers. Use an isolated directory when that cannot be guaranteed. Workcopy does not provide cache keys, eviction, concurrency control or an isolation boundary for linked contents.

```sh
workcopy unshare daily --path packages
```

`unshare` removes only the registered link after checking its target. Lane destruction also retains the target. Deleting a lane is not dependency-cache maintenance.

## .NET solution filters

```text
workcopy solution NAME --solution Product.sln
```

This writes a content-addressed `.slnf` below `.workcopy/views`, referring to the original `.sln` and materialized .NET projects. The source solution is not rewritten, and the generated view is not promoted as source. Build the printed filter path with your own .NET tooling.

Only `.sln` input is handled. `.slnx`, full MSBuild evaluation and build validation are not implemented. A structurally valid filter can still need imports, SDKs or projects not in the selected profile.
