# Agent operation

For MCP hosts, start with the [MCP guide](mcp.md). Pass the absolute checkout as `repo` to bootstrap. Retain its returned `scope.repo` and `scope.root` in every worker handoff and lane call; one server can serve multiple repositories without changing defaults. Bootstrap, the guide resource and controller/worker prompts make the workflow discoverable. Handoff requirements and safety rules below apply to both MCP and CLI agents.

MCP agents use the typed tools and check response `schemaVersion` and `exitCode`. Built-in MCP controller/worker prompts are described in the [MCP guide](mcp.md#say-use-workcopy-mcp).

The commands and copy-ready prompts below use the CLI. Use `--json` and check both the process exit code and response `exitCode`. Treat stderr as diagnostics, not as the machine contract. The utility has no interactive approval prompt; approval is represented by explicit arguments.

Start with `doctor`, `discover` and a create dry-run. Create a separate lane for each independently writing agent. Edit only its source paths. Workcopy locks its own mutations, not arbitrary writes by editors or other agents.

Read `status` before sync, resolution, review or destruction. When conflicts exist, use their current tokens. Fetch `base`, `local` and `upstream` content through bounded `content` calls when needed. Use the version hashes to check the assembled bytes. Do not invent paths, tokens or successful results.

Conflict-content reads share the lane writer lock so object cleanup cannot race the read. A concurrent mutation may therefore make `content` return exit code 3 (`workspace-busy`); wait for that operation to finish, then reread status and retry with the current token.

Resolve a conflict only after deciding on its content. Use `--from-file` for a reviewed merge result. A stale local-file warning requires inspecting the new bytes before passing `--expect-local`; blindly acknowledging the current hash defeats that check.

After a successful `merge`, inspect the separate review's tracked diff and untracked additions. A commit requires owner-authorized `--approve`, the exact prepared receipt and a meaningful message. Workcopy never turns that approval into a push or main-branch integration.

When a precondition blocks progress, preserve the lane. Do not retry with `--force`, edit metadata, delete a lock/journal or attach the review HEAD to main to work around it. Use `recover` only for a recorded incomplete operation.

A clean status is not a security or build certificate. Ignored files can contain valuable local data and are protected separately during ordinary deletion. Shared dependency warnings must be surfaced, not suppressed. Report any build or inference result as a separate action with its own evidence.

## Several agents working at once

Use one lane per independently writing agent. A lane is an ordinary source directory, not a Git checkout or a security sandbox. Workcopy's project selection controls which source it imports and tracks; it is not a write allowlist. An agent with ordinary filesystem access can still edit the original checkout, another lane, or paths outside its selection. Use the host's filesystem or agent sandbox when those writes must be prevented.

## Human review and integration

Workers may edit, validate, reconcile and prepare a review. The human remains responsible for inspecting that review and deciding whether, when and how to integrate it. A review receipt proves what Workcopy prepared; it is not an integration result. No Workcopy command pushes or integrates changes into the original branch.

Keep mutable build outputs private to each lane. They may duplicate across lanes and can exceed Workcopy's source storage estimate; include them in the task's disk budget and clean only disposable outputs. Never point concurrent lanes at the same `bin`, `obj`, generated-source or coverage directory. Workcopy's `share` creates a writable link and does not coordinate writes to the target. A package download cache may be shared only when its package manager documents concurrent use and the controller confirms that the runtime and lockfiles match; otherwise use separate caches. Many managers already keep caches outside the lane, so no Workcopy link is needed.

Create the lanes one at a time from the same locally available branch tip, before any agent starts editing. Workcopy does not fetch, so first update the branch yourself if needed. Keep that branch unchanged until all lanes are created. For example, with one lead lane and three project lanes:

```sh
REPO=/absolute/path/to/repository
ROOT=/absolute/path/to/workcopy-lanes
BRANCH=master # replace with the actual locally available integration branch
workcopy doctor --json
workcopy discover --repo "$REPO" --json
workcopy create lead --repo "$REPO" --root "$ROOT" --branch "$BRANCH" --all --dry-run --json
workcopy create agent-api --repo "$REPO" --root "$ROOT" --branch "$BRANCH" --project src/Api --dry-run --json
workcopy create agent-search --repo "$REPO" --root "$ROOT" --branch "$BRANCH" --project src/Search --dry-run --json
workcopy create agent-ui --repo "$REPO" --root "$ROOT" --branch "$BRANCH" --project src/Ui --dry-run --json
```

After checking the dry-runs and assigning ownership, repeat the four create commands without `--dry-run`, still sequentially. Use the actual local integration branch and project IDs or paths reported by discovery. Start each agent with its own returned absolute `workspace.path` as its current directory. Have each agent begin with `workcopy status --json` and verify that `workspace.path` is exactly its assigned lane. When invoking later commands from the original checkout instead of the lane, pass the lane name and the same `--repo` and `--root` again; for example, `workcopy status agent-api --repo "$REPO" --root "$ROOT" --json`. Explicit flags override lane auto-detection.

Root-level tracked files are included in every project lane by default, subject to exclusions. Assign one owner for files such as the README, solution, shared build settings and repository configuration. Do not have multiple agents independently edit these shared files. Keep each agent's writes within its assigned project paths; unselected edits may not appear in lane status or review.

Agents may edit separate lane directories concurrently. Workcopy mutations are locked per lane, so do not let two agents mutate the same lane at once. Lane creation, review preparation, recovery and destruction use a workspace-root lock and can report `workspace-busy`; run those operations one at a time and do not work around the lock. Review commits and other lane mutations use the lane lock. Shared dependency links are writable by design: do not let concurrent agents use the same shared dependency target unless they coordinate its writes.

Each lane has a separate review; `merge` prepares one or reuses its existing review when it is still current. Inspect each complete diff and its untracked files, then integrate review commits using the repository's normal Git process. After integration advances the selected branch, sync the other lanes before preparing or refreshing their reviews. A Workcopy review commit is not itself integration into the selected branch.

The lane directory and its initial status check help the agent stay oriented; Workcopy cannot guarantee that an unrestricted agent stays there. If a write to the base checkout would be unacceptable, enforce that with the execution host's filesystem permissions or sandbox and verify those permissions independently.

Before handing a lane to an agent, verify that its execution host can access the absolute lane and workspace-root paths. The host may require the directory to be added to a project/workspace filesystem allowlist, and the operating-system ACL must also grant the agent's run-as account or group the needed access. These controls are independent and vary across Codex, ChatGPT and other agent hosts. Commands that inspect or sync the source also need access to the source checkout and Git common directory. Start the agent in its assigned lane and have it run `workcopy status --json`; stop and correct the host access configuration if it cannot read the lane. Workcopy cannot add paths to a host allowlist or grant host-specific identities automatically.

On Windows, grant only the appropriate host account/group access to the intended workspace root. An inheritable Modify grant on a shared root gives every member of that group access to all lanes beneath it; use the host's per-project sandbox controls or narrower lane ACLs when agents must not access one another's lanes. See [agent access troubleshooting](troubleshooting.md#an-agent-cannot-access-a-lane) for the separate ACL and sandbox steps.

## Copy-ready controller prompt

Fill in every bracketed value before assigning work. Use the locally available branch tip; Workcopy does not fetch. Select non-overlapping write paths where possible, and assign one owner for shared root files such as solutions, build settings and repository configuration.

```text
You are the controller for independent Workcopy lanes.

Repository: [absolute source checkout path]
Workspace root: [absolute private local directory outside every Git checkout]
Source ref: [locally available branch or commit]
Workcopy executable: [absolute executable path or `workcopy` on PATH]

Before assigning work:
1. Run `workcopy doctor --json` and `workcopy discover --repo "[repository]" --json`.
2. Choose project IDs/paths from discovery and the repository's committed configuration. Do not select every project just to conceal unknown build dependencies.
3. For each worker, choose a unique lane name and run `workcopy create [lane] --repo "[repository]" --root "[workspace root]" --branch "[source ref]" --project "[project ID or path]" --dry-run --json`. Review the selection, then repeat without `--dry-run`.
4. Create all lanes from the same source ref before any worker starts editing. Create them sequentially because creation uses the workspace-root lock.
5. Give each worker the exact handoff fields below. Do not start two writers in one lane or assign overlapping paths without naming the shared-file owner.

Worker handoff:
- Repository identity and source ref:
- Lane name, absolute lane path and workspace root:
- Selected project IDs and paths:
- Allowed write paths:
- Excluded paths and files owned by another worker:
- Exact build and test commands, working directories, and expected lane-local output locations:
- Dependency policy: whether restore/network access is allowed; approved shared stores or directories; any restore command that is permitted:
- Required Workcopy commands and who may resolve conflicts or prepare the review:
- Forbidden actions: no Git checkout/reset/commit/push inside the lane; no edits to `.workcopy` or workspace registry metadata; no replacing repository AGENTS.md or project instructions; no switching to the original checkout to bypass a blocked lane operation; no `--force` or journal/lock deletion.

Monitor each lane with `workcopy status [lane] --repo "[repository]" --root "[workspace root]" --json`. Do not mutate a lane while its worker is editing. When a conflict is reported, use its current token and bounded `workcopy content` calls; coordinate one resolution at a time and preserve every remaining conflict.

When the worker is ready, run `workcopy merge [lane] --repo "[repository]" --root "[workspace root]" --json` to prepare a separate review. Inspect its tracked diff and untracked additions. Record the review receipt and path, then give them to the human owner. Do not pass `--commit` or claim integration. A review is not upstream integration; report integration only after the owner confirms the intended branch contains the reviewed change.

After the owner confirms integration, sync affected lanes before refreshing reviews. Destroy a lane only after its local contribution is clean and `workcopy destroy [lane] --repo "[repository]" --root "[workspace root]" --dry-run --json` shows the intended result. Resolve any blocker by preserving the lane and reporting it, not by bypassing its guard.
```

## Copy-ready worker prompt

The controller must supply the exact lane identity, write scope, validation commands and dependency policy. Workcopy project selection controls imported/tracked source; it is not a write allowlist.

```text
You are a worker assigned to one Workcopy lane.

Repository identity: [source checkout path and source ref]
Lane name: [lane name]
Assigned lane path: [absolute lane path]
Selected projects: [IDs and paths]
Allowed write paths: [explicit paths]
Excluded paths: [paths you must not edit]
Build/test commands and working directories: [exact commands]
Dependency policy: [restore/network permissions and approved stores]
Review owner: [controller or human owner]

1. Start in the assigned lane and run `workcopy status --json`. Confirm that the returned `workspace.path` exactly matches the assigned lane. Stop and report a mismatch or access error.
2. Read the repository's own AGENTS.md and project instructions. Edit only the allowed paths. Keep generated files and build outputs inside the lane or the explicitly approved isolated location. Do not restore packages, download dependencies, run lifecycle scripts, or mutate a shared dependency directory unless the handoff allows it.
3. Run only the supplied build and test commands. Report each exact command, working directory and process exit status. If prerequisites or commands are missing, report a blocker instead of guessing or touching the source checkout.
4. If asked to reconcile, run `workcopy sync --json`, inspect the returned conflicts and use their current tokens. Retrieve content with `workcopy content "[conflict token]" --side local --offset 0 --limit 65536 --json`; choose `base` or `upstream` when needed, and keep every limit at or below 65,536. Assemble chunks and check the reported SHA-256 when retrieving the complete side. Resolve only after choosing the result, for example `workcopy resolve "[repository-relative path]" --expect-conflict "[conflict token]" --use-local --json`. Never hide or discard another conflict.
5. Before handoff, run `workcopy status --json`. If assigned to prepare a review, run `workcopy merge --json` and report the returned review receipt ID and review path. Do not commit, push or integrate it.

Forbidden inside the Git-free lane: `git checkout`, `git reset`, `git commit`, `git push`; changing `.workcopy` metadata or workspace registry files; replacing repository AGENTS.md or project instructions; deleting locks or journals; retrying with `--force`; or switching to the original checkout to bypass a blocked operation. A blocked operation means preserve the lane and report the exact error and current status.

Return:
- Changed paths and confirmation that each is within the allowed paths
- Exact build/test/reconciliation commands, working directories and exit statuses
- Final `workcopy status --json` exit status and unresolved conflict tokens
- Blockers and the recovery steps attempted, if any
- Review receipt ID and review path, if prepared
- Integration status: `not integrated` unless the human owner confirms the reviewed change is present on the intended branch
```

## Validate a selected .NET project in its lane

Workcopy copies committed source and project relationships. It does not evaluate all MSBuild conditions, restore NuGet packages, build a project or guarantee where custom targets put their outputs. Configure project references and shared inputs in the repository, then inspect the create dry-run before handing the lane to an agent.

For a repository with a committed `daily` profile that selects the test project and includes its dependencies, create and enter the lane:

```powershell
$repo = 'C:\path\to\source-checkout'
$root = 'D:\path\to\workcopy-lanes'
workcopy discover --repo $repo --json
$preview = workcopy create agent-api --repo $repo --root $root --profile daily --dry-run --json | ConvertFrom-Json
$preview.plan.files # inspect selected paths before applying
$created = workcopy create agent-api --repo $repo --root $root --profile daily --json | ConvertFrom-Json
Set-Location $created.workspace.path
dotnet --version
$filter = (workcopy solution --solution Product.sln --json | ConvertFrom-Json).plan.files[0]
```

The filter path is relative to the lane and appears in `plan.files[0]`. It references only projects that the lane materialized. The selected project and dependency closure must include the test project, its project references, and committed root inputs such as `global.json`, `Directory.Build.props`, `Directory.Build.targets`, `Directory.Packages.props` and the `.sln` file. If relationships are conditional or otherwise not discovered, declare them in `.workcopy.json` and verify the dry-run selection.

Only run restore when the handoff explicitly permits the required package feeds and cache location. Keep the package cache separate from the lane, and inspect the repository's build properties before relying on the default `bin` and `obj` locations:

```powershell
$env:NUGET_PACKAGES = Join-Path $env:TEMP 'workcopy-nuget-agent-api'
dotnet restore $filter --packages $env:NUGET_PACKAGES
dotnet build $filter --no-restore --configuration Debug
dotnet test $filter --no-build --no-restore --configuration Debug
workcopy status --json
```

Check that build and test output names the selected projects and that `dotnet test` reports at least one executed test. A zero exit by itself is not proof that a solution configuration built any projects. Workcopy's test fixture verified a `.slnf` with a project reference and central package versions; its `Directory.Build.props` directed outputs into an ignored `.workcopy-build` directory in the lane. Your repository controls its own output paths and ignore rules, so verify them before running the build.

`workcopy solution` accepts `.sln` files. For a repository that only has `.slnx`, run the selected `.csproj` directly or provide a supported `.sln`; Workcopy does not generate a filter from `.slnx`.

The opt-in .NET acceptance test performs a NuGet restore in a disposable fixture. Run it only when network restore is approved and a .NET SDK is available:

```powershell
$env:WORKCOPY_DOTNET_LANE_ACCEPTANCE = '1'
go test ./tests/acceptance -run '^TestSelectedDotNetProjectBuildsAndTestsInsideLane$' -count=1
```

The supplied build and test commands must match the selected project and available SDKs. Workcopy does not identify every build input, run builds, restore packages, or guarantee lane-local output paths for arbitrary build systems. Keep these details explicit in each handoff.
