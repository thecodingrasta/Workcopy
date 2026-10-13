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

## Prefer native package stores

A package manager often keeps downloads outside the lane, so lanes running as the same user may already reuse one cache without a Workcopy link. Workcopy does not configure package managers, run restores or assume that an installed dependency tree is immutable.

A cache may be shared across lanes only when that package manager documents safe concurrent use and the lanes use compatible runtimes and lockfiles. For example, Go documents concurrent access to its module cache and build cache. NuGet coordinates concurrent cache access through its shared `NuGetScratch` directory; its documentation warns that it does not lock concurrent restores of the same project. Follow the requirements for the package-manager version in use. Workcopy does not verify cache safety or coordinate writes to a shared target. If the concurrency contract is unknown, keep the cache separate per lane. See the [Go module cache](https://go.dev/ref/mod#module-cache), [Go build cache](https://pkg.go.dev/cmd/go#hdr-Build_and_test_caching) and [NuGet cache guidance](https://learn.microsoft.com/nuget/consume-packages/managing-the-global-packages-and-cache-folders).

Build outputs, compiler intermediates and virtual environments can depend on absolute paths, runtime versions or project settings. Keep them lane-local unless their sharing contract has been independently checked. A directory name is not proof that its contents are safe to share.

Keep mutable outputs such as `bin`, `obj`, generated source, coverage data and temporary build products private to each lane. These outputs can be duplicated across lanes and are not included in Workcopy's source-allocation estimate or capped by its reserve. They still reduce free space, which can block later Workcopy operations. Give each lane a storage budget and use the build tool's clean command only for outputs confirmed disposable. If a build tool supports relocating outputs, configure a unique output directory per lane. Never point concurrent lanes at one writable output directory: source versions and build settings can differ, and one build can replace another lane's artifacts. Workcopy does not share or deduplicate build outputs automatically.

## Explicit sharing

The command form is:

```text
workcopy share NAME --path packages --target DIRECTORY --dependency-identity IDENTITY --trust-shared
```

`DIRECTORY` must be an existing real local directory, outside the workspace root and not an ancestor exposing the repository or lanes. `packages` is the destination relative to the lane. It must not already exist, intersect tracked source or overlap another mapping.

`IDENTITY` is your description of the compatible dependency environment, such as the package-manager/runtime version and lockfile identity. It is not a secret or a verified compatibility certificate.

Workcopy does not determine whether the destination is a package cache or a build output. Keeping build outputs private is an operator rule, not a command guard. The recorded identity and manifest fingerprints do not make a shared target safe for concurrent writes.

Workcopy records the mapping, the platform and hashes of recognised dependency manifests/lockfiles. `status` warns about input drift or a missing/retargeted mapping. It does not hash gigabytes of installed packages or prove that two lockfiles imply identical tool behaviour.

The link is a symlink on Linux/macOS and a junction on Windows. Windows targets must use a local drive, not a UNC share. Failure is explicit; there is no marker-file fallback and no automatic large copy. Each call links one lane-relative path to one existing directory. Linking the same target from several lanes gives all of them writable access to the same files; it does not combine separate build outputs or make a cache safe. Use it only for a dependency directory whose tool-specific concurrency and compatibility rules you have checked. Never use it to pool `bin`, `obj` or other mutable build outputs.

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
