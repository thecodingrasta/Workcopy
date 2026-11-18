# Workcopy Agent Plugin Publication Guide

**Research checked:** 18 November 2026 against the official product documentation linked below. Marketplace rules can change; re-open the linked vendor page before each submission.

## Publication readiness

Workcopy has six canonical Agent Skills, a portable Agent Plugins 1.0 package, native package layouts for the clients that need different configuration, repository marketplace catalogs for Claude Code, Cursor, and GitHub Copilot, and the Go-native `agentdist` validator and packager. The packages reuse Workcopy's existing Go CLI and stdio MCP server. The plugin runs local stdio MCP and supports review preparation. The approved batch is landed through the local CLI.

The package definitions are in `agent-distribution/workcopy/agentdist.json`. The portable root is `agent-distribution/workcopy/plugin/`. Native adapters live in `agent-distribution/workcopy/adapters/`. The six canonical skills and their progressive references live under `plugin/skills/`; Claude Code and Cursor marketplace source trees contain generated skill and license notice copies that `agentdist validate` compares byte-for-byte with the canonical source. The portable Git source also contains synchronized license notices. Each Git-hosted native source has its own setup README and a concrete manifest version, so installing from the repository does not rely on ZIP-time file injection or version rendering. See the [provider implementation guides](providers/README.md) for official requirements and comparisons.

The toolkit is product-configurable, not Workcopy-only. Its definition declares the product license, root-level notice files, optional MCP tool prefix and count, and optional CLI name/help source. The checked-in Workcopy configuration specifies `GPL-3.0-or-later`, `workcopy_`, and `workcopy`; an isolated test fixture validates a second Go module using MIT, `acme_`, and `sample-cli`.

Package from a clean checkout of the exact reviewed commit. Set `version` to the reviewed plugin version without its leading `v` and `output` to a fresh directory for that commit. The normal release workflow builds both the executable and these ZIPs from the same tag.

```sh
go run ./tools/agentdist validate --release-version "$version"
go run ./tools/agentdist package --version "$version" --output "$output"
```

The checksummed ZIP names are:

| Artifact | Use |
| --- | --- |
| `workcopy_agent_plugin_v<VERSION>.zip` | Portable Agent Plugin with skills and local stdio MCP; use for Devin, Hermes, and compatible Agent Plugins clients. |
| `workcopy_agent_kiro-power_v<VERSION>.zip` | Portable Agent Plugin packaged as an installable Kiro Power. |
| `workcopy_agent_claude-code_v<VERSION>.zip` | Claude Code native plugin manifest and `.mcp.json`, plus canonical skills. |
| `workcopy_agent_antigravity_v<VERSION>.zip` | Antigravity native plugin manifest and MCP configuration, plus canonical skills. The marketplace logo is still required. |
| `workcopy_agent_cursor_v<VERSION>.zip` | Cursor native plugin manifest and MCP configuration, plus canonical skills. |
| `workcopy_agent_openai-skills-only_v<VERSION>.zip` | Separate OpenAI skills-only plugin identity; no MCP server is bundled in this fallback. |
| `workcopy_agent_opencode_v<VERSION>.zip` | OpenCode skill files and an `opencode.json` MCP example. |
| `workcopy_agent_kiro_v<VERSION>.zip` | Kiro skill files and a native MCP settings example. |
| `workcopy_agent_vscode-copilot_v<VERSION>.zip` | VS Code/Copilot skills and `.vscode/mcp.json` example. |
| `AGENT_PLUGIN_SHA256SUMS` | SHA-256 checksums for the nine ZIPs above. |

`agentdist package` refuses to overwrite existing files. Use a new empty output directory or move reviewed artifacts aside before regenerating. The ZIPs contain the manifests, skills, README, `LICENSE`, `COPYRIGHT`, and marketplace metadata; **they do not contain the Workcopy executable**. Every MCP adapter expects a matching `workcopy` release on the MCP host's `PATH`. Keep the CLI and plugin release versions aligned and check `workcopy version` before connecting a host.

The release workflow packages from an existing reviewed Git tag after the normal native validation gate. It attaches the ZIPs, both checksum lists, the platform CLI binaries, and corresponding source to a **draft** GitHub Release. It does not publish that draft or submit any marketplace listing. CI has native jobs for Linux amd64/arm64, Windows amd64, and macOS amd64/arm64; the release workflow also smoke-tests Windows arm64. These are configured checks, not results for this uncommitted change. A CI run on the reviewed source commit is still required before release.

### Validation evidence and remaining checks

`go run ./tools/agentdist validate` checks the portable manifests, native adapter layouts, skill metadata/resources, CLI references, MCP tool names and argument examples, generated marketplace copies, version consistency, package inputs, licenses, and source paths. `go test ./tools/agentdist ./tools/dev -count=1` exercises the distribution and release-tool regressions. `go run ./tools/dev validate` is the repository-wide gate: formatting, distribution validation, `go vet`, the race-enabled Go suite, a native Workcopy build, and CLI JSON smokes.

Record validation against the exact revision, package checksums, Workcopy version, host version, OS and architecture. Keep the Windows and Linux/WSL platform runs with the engineering handover. A provider validator confirms what that loader can read. Use the manual workflow below to qualify installation, tool invocation, and approved landing in the application.

The following work is still required before the indicated public listing:

- Antigravity's marketplace requires a Workcopy-owned square logo asset. The manifest intentionally contains no fabricated logo.
- OpenAI accepts local-MCP submissions only from allowlisted organizations. Its package guidance also asks for the local server and dependencies to be included, while this package relies on `workcopy` being preinstalled on `PATH`. Obtain reviewer confirmation for that prerequisite or use the separate skills-only plugin identity.
- The actual harness acceptance tests and a CI run on the final reviewed commit remain to be done.

### Verify artifacts

On Windows PowerShell, set `$artifactDirectory` to the exact candidate output directory and verify the integration ZIPs against its checksum list.

```powershell
Set-Location -LiteralPath $artifactDirectory
Get-Content .\AGENT_PLUGIN_SHA256SUMS | ForEach-Object {
    $parts = $_ -split '\s+', 2
    $expected = $parts[0].ToLowerInvariant()
    $filename = $parts[1].TrimStart('*')
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $filename).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { throw "SHA-256 mismatch: $filename" }
}
```

From the candidate output directory, use `sha256sum -c AGENT_PLUGIN_SHA256SUMS` on Linux or `shasum -a 256 -c AGENT_PLUGIN_SHA256SUMS` on macOS. Compare the checksum list itself with the one attached to the reviewed release artifact when checking provenance. A checksum file detects accidental change relative to that list; it is not a publisher signature.

The release CLI uses `SHA256SUMS` for platform executables. Verify the exact platform filename from that file before installing it. On Windows amd64, set `$tag` to the reviewed release tag and run the following from the download directory.

```powershell
$asset = "workcopy_${tag}_windows_amd64.exe"
$entry = Get-Content .\SHA256SUMS | Where-Object { $_.EndsWith("  $asset") }
if (!$entry) { throw 'Missing Workcopy binary checksum' }
$expected = ($entry -split '\s+')[0].ToLowerInvariant()
$actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $asset).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'Workcopy binary checksum mismatch' }
```

Download the binary and `SHA256SUMS` from the same reviewed GitHub Release. Install into a user-owned directory only after verifying the full digest. Do not replace an existing binary without deciding to do so. Add the directory to `PATH` as a separate user-controlled step, then restart the harness and run `workcopy version` and `workcopy doctor`. Git 2.43 or newer is required for repository operations; Go 1.27.4 or newer is needed only to build Workcopy from source.

Use a binary that matches the **harness process** OS and architecture. In WSL, either run the Linux binary with Linux paths and a WSL-native lane root, or use the Windows binary with Windows paths. Do not mix `C:\...` and `/mnt/c/...` between a Windows and Linux process. Under cloud harnesses, Workcopy runs in the cloud process environment and can only see that environment's mounted checkout. This plugin does not connect that environment to another computer.

## Security and operating boundaries

Plugin installation alone does not create, delete, or modify lanes. Start with read-only diagnostics. Workcopy enforces operation-specific scope, ownership, locking, conflict, recovery, and cleanup guards. The host or agent must obtain user consent and request previews before applying changes. The server accepts an apply with `dryRun: false` without a prior preview call. Batch preparation writes candidate objects to the source repository's Git object database while preserving its checked-out branch and index. The approved batch is landed through the authorized local CLI.

Workcopy lanes are workflow boundaries, not OS sandboxes or write allowlists. The host and OS permissions govern what the agent can access. A local stdio MCP server runs where the host process runs; local configuration does not give a web/cloud agent access to the user's separate local filesystem.

### Lane storage and uninstall

The portable and Antigravity packages start `workcopy mcp` with normal Workcopy storage resolution. Configure a durable location with `WORKCOPY_ROOT` in the Workcopy settings file or use the default repository-specific sibling directory. Keep lane and review storage outside the plugin install and data directories. A host can remove both on uninstall.

If an earlier build used `${PLUGIN_DATA}/lanes`, record the exact `scope.repo` and `scope.root` from a working session before updating or uninstalling. Finish or preserve pending work while that root is accessible. To resume in place, configure the local server with `workcopy mcp --repo "REPO" --root "ROOT"` using the recorded absolute paths and keep it installed until the work is preserved independently. For a filesystem backup, stop writers, preserve the entire root and its Workcopy metadata, and restore it at the same absolute path if needed. Workcopy records absolute paths and will not automatically discover or migrate lanes from the old root.

## Harness installation and manual testing

All commands below are examples for a local acceptance environment. Review and merge configuration entries rather than replacing an existing file. Use a disposable Git repository and private temporary Workcopy root for mutations; never run cleanup tests against real lanes.

### ChatGPT and Codex

OpenAI's public plugin directory is shared by ChatGPT and Codex, but local stdio MCP plugins are marked desktop-only. The full portable package is `workcopy_agent_plugin`; use it for a local-MCP submission only after OpenAI confirms organization eligibility and confirms that requiring the user to install the Workcopy CLI separately is acceptable. In the submission portal choose **Create plugin → Skills only**, then upload the ZIP that includes `mcp.json`, as OpenAI's desktop-only procedure directs. It is not a hosted remote server.

For a skills-only listing, upload the separate `workcopy_agent_openai-skills-only` archive. It deliberately has a different plugin name and no MCP configuration. Keep that public listing skills-only; do not later add MCP to it. If a full local-MCP submission is approved, submit the distinct `workcopy_agent_plugin` package as a separate listing.

For Codex without installing a plugin, copy the contents of the archive's `skills/` folder into the project's `.agents/skills/` or the user's `~/.agents/skills/`. Configure the executable in Codex's user `config.toml` or a trusted project configuration, merging the entry from [the MCP guide](mcp.md#codex). Example:

```toml
[mcp_servers.workcopy]
command = 'C:\tools\workcopy.exe'
args = ['mcp', '--root', 'C:\workcopy-lanes']
enabled = true
startup_timeout_sec = 20
tool_timeout_sec = 180
```

On Linux/macOS substitute native paths. On WSL, configure the Windows and Linux Codex hosts separately with their matching binaries and path syntax. Restart the app. Confirm the Workcopy MCP tools are visible and ask for read-only setup checks; do not use lane creation as a connection test.

For a local marketplace in ChatGPT desktop, use the repository's Claude-compatible catalog or the complete native catalog example in the [OpenAI provider guide](providers/openai-codex.md). Both are local routes for testing or private distribution.

**Publish:** [OpenAI plugin portal](https://platform.openai.com/plugins). Public plugins are reviewed before publication; developer/business identity and portal submission permissions are required. For local MCP, OpenAI currently limits submissions to approved partners: find the submitting organization ID in Platform settings, send it to the OpenAI representative, and wait for allowlisting. Test a clean desktop installation. If the portal rejects the local executable prerequisite, stop and use the skills-only package or resolve a platform-bundling approach with OpenAI. OpenAI's docs say local-MCP submission should include the local configuration and server dependencies, so the current PATH prerequisite is a real review question.

OpenAI distinguishes local/personal/repository marketplace sources for authoring and private distribution from its universal public plugin directory. A project or personal marketplace is not a public listing or a workspace-wide publisher approval. The public directory is the shared ChatGPT/Codex listing. OpenAI documents automatic checks for eligible MCP server updates after initial publication; changes to skills or plugin metadata require another ZIP. Do not assume that automatic hosted-server updates cover a local binary or local launch configuration. Use the portal's version and rollback controls for the specific listing.

Official references: [desktop-only plugins](https://developers.openai.com/plugins/deploy/desktop-only), [submission](https://developers.openai.com/plugins/deploy/submission), [package plugins](https://developers.openai.com/plugins/build/plugins), and [Codex MCP](https://developers.openai.com/codex/mcp).

### Claude Code

Use `workcopy_agent_claude-code` for the native `.claude-plugin/plugin.json` and `.mcp.json` layout. For local testing from the repository root:

```sh
claude plugin validate .
claude plugin marketplace add .
claude plugin install workcopy@workcopy-tools
claude plugin list
claude plugin details workcopy
```

In a Claude Code session, confirm the six skills appear under the `workcopy` namespace and the MCP server starts with the expected 20 Workcopy tools. The repository marketplace at `.claude-plugin/marketplace.json` points to the native adapter source, whose generated skills are checked against the canonical skill files. `claude plugin marketplace remove workcopy-tools` unregisters that marketplace. Uninstall the plugin separately if needed.

For a temporary directory-only test, Claude Code also supports `claude --plugin-dir ./agent-distribution/workcopy/adapters/claude-code`; run it from the Workcopy repository and confirm the MCP server and skills load. Configure the `workcopy` executable in the same environment as Claude Code. The packaged native config uses `workcopy mcp`; configure the owner's preferred private Workcopy root in Workcopy settings before creating a test lane.

**Publish:** choose either a custom Claude Code marketplace (the repository manifest above) or Anthropic's public directory; they are distinct. The custom marketplace is a Git-hosted catalog controlled by its owner and has no Anthropic review. For the public directory, submit through [Claude's directory manager](https://claude.ai/directory/manage). The plugin must be in a GitHub repository; connect a GitHub account with push access. The repository can remain private during validation/submission but must be public before the listing goes live. The submitter also needs a plan/role allowed to submit. Anthropic tests submissions/commits and asks for product and data-handling details before human review. Select the plugin path `agent-distribution/workcopy/adapters/claude-code`, not the portable root. Fix portal feedback in source and resubmit. Use the directory manager to update or remove a listing; do not treat deleting the Git source as a rollback. See [submit a plugin](https://claude.com/docs/plugins/submit) and [Claude Code marketplaces](https://code.claude.com/docs/en/plugin-marketplaces).

### Google Antigravity

Use `workcopy_agent_antigravity`. The package includes `.antigravity-plugin/plugin.json`, `mcp_config.json`, a README, and the canonical skills. Its MCP command uses the normal Workcopy lane root. Install or validate the unpacked ZIP because the source template receives its README and skills during packaging.

After adding the official logo asset to the adapter source and the `logo` field to its manifest, test with:

```sh
agy plugin validate --strict ./unpacked-antigravity-package
agy plugin install ./unpacked-antigravity-package
agy plugin list --json
```

Confirm the three suggested prompts, skills, local MCP server, and a read-only `workcopy_doctor` call. Test user-requested lane operations only in a disposable repo. The strict publication test is currently blocked by the missing brand logo; do not count the current archive as marketplace-ready.

**Publish:** the [Antigravity Marketplace](https://antigravity.google/docs/build-plugins) currently uses a [partner intake form](https://forms.gle/2EX5RFYPoJe1UgxR9). The published guide requires a complete manifest, README, bundled square logo, strict local validation, and partner submission. Provide the real Workcopy logo; the current `logo` field is omitted intentionally. The form/review timeline and post-publication rollback process are not specified in the public docs, so obtain those details from the partner response. Submit a new reviewed package for changes and follow the marketplace instructions for withdrawal if needed.

### Cursor

Use `workcopy_agent_cursor`, which contains `.cursor-plugin/plugin.json` and a Cursor-compatible `mcp.json` without Agent Plugins `${PLUGIN_ROOT}` or `${PLUGIN_DATA}` placeholders. Cursor does not expand those standard variables in MCP configuration; use the native adapter here.

For local testing, place the extracted package directory under `~/.cursor/plugins/local/workcopy` (Windows: `%USERPROFILE%\.cursor\plugins\local\workcopy`), restart Cursor or run **Developer: Reload Window**, then open **Customize** and verify the plugin's skills and MCP server. Enterprise policy may disable local plugin imports. Confirm the server in Customize, then check its tools.

The repository's `.cursor-plugin/marketplace.json` is a Git-hosted multi-plugin catalog pointing to the native adapter source. Cursor's public Marketplace submission is separate: push the reviewed plugin source to a public Git repository and submit the repository at [cursor.com/marketplace/publish](https://cursor.com/marketplace/publish). Cursor manually reviews open-source plugins and updates. The README and valid manifest are present; a logo is optional but recommended. Correct review feedback in the source, regenerate the matching archive, and resubmit. A team marketplace is separately managed in the Cursor admin dashboard; it is not the public Marketplace. See the [plugin guide](https://cursor.com/docs/plugins) and [submission reference](https://cursor.com/docs/reference/plugins).

### Devin

Use the portable `workcopy_agent_plugin` archive. Extract it into a local directory and install it in Devin CLI:

```sh
devin plugins install --local ./workcopy_agent_plugin
devin plugins list
devin plugins info workcopy
```

To install from a GitHub repository subdirectory instead, Devin documents `devin plugins install owner/repo#path`; for Workcopy that path is `agent-distribution/workcopy/plugin`. Prefer the versioned ZIP for a fixed release. Update or remove with `devin plugins update workcopy` and `devin plugins remove workcopy`. Test that Devin's MCP process sees the same `workcopy` executable and checkout filesystem as the task.

Devin supports the portable Agent Plugins root layout and MCP. Install plugins from a local folder, GitHub repository, Git URL, or repository subfolder; there is no general reviewed public plugin directory. For team-controlled distribution, an organization or enterprise admin can add a repository under **Customize → Plugins → Add plugin → From repository**, then select its scope. This is an admin-managed distribution path, not a public listing. A Devin Cloud session can operate only on its own execution environment and mounted repository, not the user's separate computer. See [Devin plugins](https://docs.devin.ai/cli/extensibility/plugins/overview) and the [team marketplace guide](https://docs.devin.ai/cli/extensibility/plugins/quickstart).

### OpenCode

Use `workcopy_agent_opencode`. It is a native skills-plus-MCP adapter, **not** an OpenCode JavaScript/TypeScript runtime plugin. OpenCode discovers its skills from the `.agents/skills/` path in the archive. Merge its `mcp.servers.workcopy` entry into the project's existing `opencode.json`/`opencode.jsonc`, or add it with the documented CLI command:

```sh
opencode mcp add workcopy -- workcopy mcp
opencode mcp list
```

For `opencode mcp add`, review the targeted project/global config location and preserve existing entries. Set `codemode: false` in that server entry as in the adapter example to expose the tools directly. OpenCode prefixes the server name, so `workcopy_doctor` appears as `workcopy_workcopy_doctor`. `opencode mcp list` should show the connection as connected. Ask the agent to invoke Workcopy's read-only doctor/bootstrap operations. OpenCode skills also support the standard `.agents/skills` compatibility directory; no Workcopy JS plugin is required. See [skills](https://opencode.ai/docs/skills), [MCP servers](https://opencode.ai/docs/mcp-servers), and [CLI commands](https://opencode.ai/docs/cli/commands).

OpenCode has no separate central reviewed marketplace submission for this adapter. Share the reviewed repository or archive and its checksum; update the source package when Workcopy releases. Remove the named MCP entry or the copied `workcopy-*` skills to uninstall. Confirm exact locations with `opencode mcp list` before editing.

### T3 Code

No T3-specific plugin or marketplace package is provided. T3 Code uses the selected provider's agent/capability protocol; Workcopy should be configured through that provider's documented MCP and skill mechanism. Use the adapter and installation steps for the chosen provider, then verify the agent can see both its skills and the 20 Workcopy tools. Do not describe T3 Code itself as having a Workcopy-native plugin. Provider and T3 support can change; see the [T3 Code ACP documentation](https://github.com/pingdotgg/t3code/blob/main/docs/user/providers-acp.md).

### Kiro

For the managed plugin-style install, use `workcopy_agent_kiro-power`. It has a portable root `plugin.json`, `mcp.json`, and canonical skills. In Kiro IDE open **Powers → Add Custom Power → Import power from a folder**, choose the extracted package root, and install. Kiro manages the MCP server with the Power. Check the installed Power and use its Workcopy setup skill before making a disposable lane.

For a manual setup, use `workcopy_agent_kiro`: copy `.kiro/skills/` into the project and merge its `mcpServers.workcopy` entry into `.kiro/settings/mcp.json` (workspace or user settings). Preserve other servers. The `workcopy` executable must be on Kiro's host PATH. For removal, uninstall the Power from the IDE Powers panel, or remove only the targeted skills and MCP entry. Kiro CLI documents `/powers uninstall <name>` and `kiro-cli powers uninstall <name>` for removal.

Kiro's curated Power registry is at [kiro.dev/powers](https://kiro.dev/powers). The public docs support custom Agent Plugins powers from GitHub and local folders but do not provide a clear self-service directory submission form or review timeline. The [publisher terms](https://kiro.dev/powers/terms/) permit Kiro to request publisher/contact information and review or remove submissions. Use GitHub/local install until Kiro confirms the current submission route. No marketplace acceptance is claimed here. See [install Powers](https://kiro.dev/docs/powers/installation/) and [create Powers](https://kiro.dev/docs/powers/create/).

Kiro Web/cloud sessions run in their own environment. A local MCP process can access only that environment's files; it does not reach a desktop checkout on another machine.

### GitHub Copilot and VS Code

Use the repository marketplace catalog at `.github/plugin/marketplace.json`, which points to the portable Agent Plugin at `agent-distribution/workcopy/plugin`. In Copilot CLI:

```sh
copilot plugin marketplace add thecodingrasta/Workcopy
copilot plugin marketplace browse workcopy-tools
copilot plugin install workcopy@workcopy-tools
copilot plugin list
```

For VS Code, add the source through the [marketplace deep link](vscode://chat-plugin/add-marketplace?ref=thecodingrasta%2FWorkcopy), confirm the prompt, then browse and install from the Customization Marketplace. Adding a marketplace only makes plugins discoverable; it does not install them. Alternatively, use `workcopy_agent_vscode-copilot` to merge the `.vscode/mcp.json` entry and copy skills under `.github/skills/`.

This is a Git-hosted marketplace catalog rather than a central GitHub-reviewed directory. Users need read access to the repository; a public repo makes the catalog accessible to everyone. Do not modify `.github/copilot/settings.json` automatically. To update, publish the reviewed repository changes, then run `copilot plugin marketplace update workcopy-tools` and `copilot plugin update workcopy`. Remove the plugin with `copilot plugin uninstall workcopy`; unregister with `copilot plugin marketplace remove workcopy-tools` after uninstalling. See [GitHub marketplace guidance](https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/create-and-share-plugin-marketplaces), [VS Code Agent Plugins](https://code.visualstudio.com/docs/agent-customization/agent-plugins), and the [Copilot CLI plugin reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-plugin-reference).

VS Code/Copilot local execution requires Workcopy on the host's PATH. A cloud agent can use Workcopy only if the executable is installed in its own environment and its checkout is available there.

### Hermes Agent

Hermes implements a documented portable Agent Plugins 1.0 subset: root `plugin.json`, skills, and stdio MCP. This is distinct from Hermes's native Python `plugin.yaml`/`register(ctx)` extension. Use `workcopy_agent_plugin`; Use a durable Workcopy lane root outside any plugin-managed storage.

For local acceptance, extract the archive into `~/.hermes/plugins/workcopy`, then validate and enable it:

```sh
hermes plugins validate ~/.hermes/plugins/workcopy
hermes plugins list
hermes plugins enable workcopy
```

Confirm the `workcopy` skills and MCP server load. Use an isolated Hermes profile if one is available; plugin enablement grants the local subprocess the host user's permissions. For a reviewed catalog install after approval, use `hermes plugins install workcopy` and `hermes plugins enable workcopy`.

**Publish:** Hermes's Plugin Catalog requires a pull request adding `plugin-catalog/workcopy.yaml` to the public Hermes repository. The catalog entry must point at Workcopy's public repository and an exact 40-character commit SHA; its `subdir` is `agent-distribution/workcopy/plugin`. It must pass catalog CI, including `hermes plugins validate`. Use the community tier; only Hermes maintainers assign official/verified status. Submit the PR after Workcopy's final source commit is public, then update the pinned SHA through another reviewed PR for each Workcopy release. To roll back, submit a catalog pin rollback or request removal/delisting through that catalog; an arbitrary Git URL install bypasses catalog review. See [portable plugin compatibility](https://hermes-agent.nousresearch.com/docs/developer-guide/plugins/), [catalog rules](https://hermes-agent.nousresearch.com/docs/user-guide/features/plugin-catalog), and [catalog submission](https://hermes-agent.nousresearch.com/docs/user-guide/features/plugin-catalog#submitting-a-plugin-to-the-catalog).

### Installation and capability check

For every harness, finish this short disposable-repository test before listing it:

1. Verify the Workcopy CLI version matches the archive's `plugin.json` version and that `workcopy doctor` sees Git 2.43 or newer.
2. Install the plugin/skills and confirm installation alone created no lane.
3. Confirm the expected six skills are discovered. For MCP-backed packages, confirm the server exposes the 21 tools in the [MCP tool table](mcp.md#tools-and-results), accounting for host prefixes. Invoke the read-only `workcopy_doctor`, `workcopy_bootstrap`, and `workcopy_discover` first. For skills-only packages, connect MCP separately before testing its tools.
4. In a disposable Git repository, preview lane creation with one explicit project, inspect the plan, then create only as part of the test. Edit and validate only in the lane. Exercise status and a deliberate sync conflict, resolve with a current token, and prepare a separate review.
5. Create several disposable lanes, leave one out of the selection, and advance the target branch. Verify preview and review preserve the checkout's HEAD, index and worktree. Prepare the selected batch through MCP, resolve any overlap with the current token and inspect the exact candidate. With a different CLI default root, invoke the returned `batchLandArgs` and add `--approve` only after approval. Verify the target reaches the approved commit, the excluded lane is omitted and unrelated staged, unstaged and untracked changes survive.
6. Uninstall with an unfinished **disposable** lane, reinstall and check that its edit and Workcopy status are still available. Finish with a cleanup preview and remove only disposable test lanes that Workcopy marks safe for cleanup.
7. Repeat on each OS/harness combination in the support claim. Run one Windows/WSL pair with the matching binaries and path syntax. For a cloud harness, confirm that the CLI, MCP server, checkout, and lane root all exist inside its own execution environment.

If the MCP server is unavailable, confirm the executable and Git are visible to the same host process, the harness was restarted after a PATH/config change, and the config syntax is correct. Do not create a lane or replace configuration to diagnose a connection failure.

## Marketplace publication and updates

| Marketplace or catalog | Submission path | Package/source | Review status |
| --- | --- | --- | --- |
| OpenAI universal plugin directory | [Plugin portal](https://platform.openai.com/plugins) | Full `workcopy_agent_plugin` only after local-MCP eligibility and packaging are approved; otherwise separate OpenAI skills-only identity. | Approved-partner allowlisting for local MCP, desktop-only behavior, portal checks and human review. |
| Anthropic Claude directory | [Directory manager](https://claude.ai/directory/manage) | GitHub repo path `agent-distribution/workcopy/adapters/claude-code`. | Account/plan eligibility, GitHub push access, test and human review; repo public before listing. |
| Cursor Marketplace | [Publish page](https://cursor.com/marketplace/publish) | Public GitHub repo with native Cursor adapter and `.cursor-plugin/marketplace.json`. | Manual review; each update is reviewed. |
| Google Antigravity Marketplace | [Partner intake](https://forms.gle/2EX5RFYPoJe1UgxR9) | `workcopy_agent_antigravity` after adding the genuine bundled logo. | Partner intake and review; public docs do not specify timing or rollback. |
| Kiro Powers directory | [kiro.dev/powers](https://kiro.dev/powers) | `workcopy_agent_kiro-power`; public GitHub/local install works before listing. | Self-service submission process not clear in published docs; confirm with Kiro. |
| Hermes Plugin Catalog | PR to `NousResearch/hermes-agent` | Catalog metadata points to Workcopy repository/subdirectory and exact commit SHA. | Human-reviewed PR and catalog CI; community tier. |
| GitHub Copilot marketplace | Workcopy's GitHub repository and `.github/plugin/marketplace.json`. | Portable plugin source. | Repository-hosted catalog; no central Marketplace review. Readers need repository access. |
| Devin team marketplace | Devin organization/team settings. | Portable plugin source or release ZIP. | Team-admin controlled; no general public reviewed directory documented. |

OpenCode and T3 Code do not have a separate Workcopy marketplace submission. Share the reviewed ZIP/repository or use the underlying provider's marketplace. Do not create a T3-specific plugin without a distinct supported interface.

### Version and rollback rules

1. When changing the product version, update `defaultVersion` in `agentdist.json`, the portable `plugin/plugin.json` and the concrete Git-source Claude and Cursor manifests. Edit canonical skills or adapter templates, then run `go run ./tools/agentdist sync-skills`. Use `--overwrite` to refresh stale declared skill and notice copies. Review the diff.
2. Run `go run ./tools/agentdist validate` and `go test ./tools/agentdist -count=1`, then the full `go run ./tools/dev validate` gate. Confirm all native CI jobs on the same reviewed commit.
3. Use the same version for the release tag, Git-source plugin manifests, ZIPs and binaries. The workflow checks the tag against the validated commit. Full release packaging runs `agentdist validate --release-version` before writing outputs. The workflow verifies checksums, runs native binary smoke tests, and creates a draft release. Publish the GitHub release only after review.
4. Install the exact CLI and plugin versions together. Upload or submit the correct harness package; keep the OpenAI skills-only and local-MCP identities separate.
5. For Git-hosted marketplaces, make the source commit public and update the catalog/repository. For Hermes, submit the new full SHA as a new catalog PR. For vendor-reviewed directories, use the portal to submit the updated release and wait for approval.
6. If a release is rejected, read the portal/CI reason, fix the source, regenerate all artifacts, and resubmit. To roll back, select the prior reviewed version through the vendor portal/catalog or restore its exact repo pin. Do not delete a plugin's data directory or Workcopy lanes as a rollback step.

The Workcopy release workflow itself leaves a draft, not a public release. This repository change does not commit, push, create a tag, publish a GitHub release, or submit any marketplace listing.

## Publication checklist

- [ ] Supply the real square Workcopy logo and update Antigravity manifest/package.
- [ ] Obtain OpenAI organization allowlisting and written confirmation about the separately installed CLI prerequisite; otherwise choose the skills-only fallback.
- [ ] Run the disposable harness acceptance test for every platform/surface being claimed; record exact app, OS/architecture, CLI version, and result.
- [ ] Run `sync-skills`, `agentdist validate`, `go test ./tools/agentdist -count=1`, and `go run ./tools/dev validate` on the final source.
- [ ] Review the repository diff and successful CI matrix on the final release commit.
- [ ] Generate platform binaries and all nine Agent Plugin ZIPs from one reviewed tag; verify both checksum lists.
- [ ] Create and review the draft GitHub Release; publish it manually only after approval.
- [ ] Publish Git-hosted marketplace catalogs or submit vendor portals in this order: GitHub/Copilot, Claude Code, Cursor, Kiro confirmation, Antigravity partner intake, OpenAI after approval, Hermes catalog PR.
- [ ] Record each listing's URL, version, source SHA, submission result, and any vendor-specific restrictions for future updates or withdrawal.
