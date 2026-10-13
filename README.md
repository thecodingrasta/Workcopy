# Workcopy

**Independent development lanes. Selected source. Human-controlled integration.**

[![Native CI](https://github.com/thecodingrasta/workcopy/actions/workflows/ci.yml/badge.svg)](https://github.com/thecodingrasta/workcopy/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/thecodingrasta/workcopy?include_prereleases)](https://github.com/thecodingrasta/workcopy/releases)
[![Licence: GPL v3 or later](https://img.shields.io/badge/licence-GPLv3%2B-blue.svg)](LICENSE)
[![Go 1.25+](https://img.shields.io/badge/source-Go%201.25%2B-00ADD8.svg)](go.mod)
[![MCP](https://img.shields.io/badge/MCP-stdio-6750A4.svg)](docs/mcp.md)

Workcopy is a cross-platform Go CLI and local MCP server for developers and AI agents working on the same repository. Each worker gets an editable directory without `.git`. The human stays in the loop: they inspect the exact contribution in a separate, unstaged Git review and decide whether and how to integrate it.

## Why it exists

Concurrent workers need independent source, predictable reconciliation and a clear review boundary. Full checkout copies duplicate unrelated monorepo projects. Shared working directories mix edits and build outputs. Agents with Git access can change the wrong branch or index.

Workcopy creates lanes from **committed snapshots**, selects requested projects and dependencies, reconciles baseline/local/upstream changes, and prepares an **unstaged human review**. It preserves the original checkout's HEAD, index and dirty files. It never fetches, pushes or integrates into the original branch automatically.

Selected source is an independent editable copy in each lane. Storage stays compact through project selection and package-manager caches that already live outside lanes. Workcopy does not guarantee copy-on-write savings or deduplicate build outputs. Each review materializes the full repository source with the lane's contribution.

## Install

Install **Git 2.43 or newer**. Go is needed only to build from source.

1. Download your platform archive and `SHA256SUMS` from [Releases](https://github.com/thecodingrasta/workcopy/releases).
2. Verify the checksum, extract it and put `workcopy` or `workcopy.exe` in a directory on your `PATH`.
3. Run `workcopy version` and `workcopy doctor`.

Before the first published release, use the [Windows amd64 candidate](prebuilt/README.md). It includes checksums and matching source. Other platform builds are available as temporary [CI artifacts](https://github.com/thecodingrasta/workcopy/actions/workflows/ci.yml) from successful default-branch runs; GitHub sign-in is required. A badge is not a claim that an unpublished release exists.

To build locally with a supported patched Go release, at least Go 1.25:

```sh
go run ./tools/dev setup
go run ./tools/dev build
```

The executable is in `dist/`. `go run ./tools/dev install` installs to `GOBIN`, or `GOPATH/bin`. Neither command changes your `PATH`.

## Use Workcopy MCP

1. Download and extract the executable from [installation](#install).
2. For Codex, paste the [example settings](docs/mcp.md#codex) into `~/.codex/config.toml`. Other hosts use the [JSON setup](docs/mcp.md#other-mcp-hosts). Replace the executable and storage paths. One server supports multiple repositories; no repository argument is required.
3. Restart the host, open your project and say **“Use Workcopy MCP for this repository. Create a lane for my task, validate inside it and prepare a review for me.”**

The agent discovers projects, checks build inputs and previews lane creation. The agent needs your approval for dependency downloads; Workcopy does not install packages. You review the result before integration. Agents can select a different lane root for each task. The host creates subagents; Workcopy manages their lanes. The [MCP guide](docs/mcp.md) explains multi-repository scope and every tool. [Controller and worker prompts](docs/agents.md) define ownership and handoffs.

Normal CLI commands support schema-versioned `--json`. MCP uses its own transport and does not take `--json`. See the [command reference](docs/commands.md).

## Your first lane

Run from an existing Git repository with at least one commit. This example copies all tracked source in a small repository. For a monorepo, use [project selection](#large-repositories) before creating lanes:

```sh
workcopy doctor
workcopy create fix-parser --all
workcopy status fix-parser
```

Open the printed lane path and edit there. Creation does not import staged, unstaged or untracked files. If required source exists only as dirty changes, preserve it or commit it to a local branch first, then create from that committed ref. Use `--root` to choose private local storage outside every Git checkout.

Inside the lane:

```sh
workcopy status
workcopy sync
workcopy merge
```

Resolve reported conflicts before repeating `merge`. It prints a separate review path and receipt. Inspect that review's Git diff before choosing how to integrate. Preparation does not commit or merge your changes. The [workflow guide](docs/workflow.md) covers approval, integration, recovery and guarded cleanup.

## Large repositories

Discover the project IDs and dependencies, then preview a lane with only the projects needed for the task:

```sh
workcopy discover
workcopy create api-fix --project src/Api --project src/Api.Tests --dry-run
workcopy create api-fix --project src/Api --project src/Api.Tests
```

Replace the example paths with project IDs or repository-relative directories reported by `discover`. Check the preview for selected files, dependencies and warnings before creating the lane. Include required tests and shared build inputs. For repeatable selections, commit a `.workcopy.json` profile and create lanes with `--profile`; see the [configuration guide](docs/configuration.md) and [25-project example](examples/monorepo.workcopy.json).

Workers build and test **inside their lanes**. Include required configuration, SDK definitions, references, tools and generated-code inputs. Workcopy does not run scripts, install packages or promise arbitrary repositories will build. Package managers often reuse a per-user download cache outside the lanes, with no Workcopy link required; follow that manager's concurrency rules. Optional `workcopy share` creates one writable link from a lane path to an existing owner-approved directory. It copies nothing and coordinates no writes. Never point concurrent lanes at the same `bin`, `obj`, generated files, coverage or other build output. Those outputs remain lane-local, can duplicate and are outside Workcopy's storage reserve. See [storage](docs/storage.md) and [lane validation](docs/mcp.md#isolated-validation-and-storage).

## Safety and support

- Lanes are workflow boundaries, **not agent sandboxes**. Host permissions must restrict access when isolation is required.
- `destroy` protects contributions, conflicts and local-only files, including ignored files. `--force` discards owned content but never bypasses ownership or deletes shared targets.
- Use local filesystems. Network shares and cloud-synchronised roots are unsupported. Windows source symlinks require creation permission.
- Custom Git filters, LFS, submodules, unsafe filenames, objects over 64 MiB and repositories over 100,000 tracked paths are rejected explicitly.

[Validation status](docs/validation.md) distinguishes native results, compile-only targets and unavailable checks. Linux/macOS native qualification is delegated to CI and contributors; it has not been run on the Windows development host. Read [SECURITY.md](SECURITY.md), [troubleshooting](docs/troubleshooting.md) and [SUPPORT.md](SUPPORT.md) before reporting problems.

## Contribute

Small reproductions, platform validation and documentation fixes are welcome. Start with [CONTRIBUTING.md](CONTRIBUTING.md). Native CI checks formatting, vet, race tests, CLI/MCP workflows, lane validation and vulnerabilities. Maintainers review and publish release drafts. No automatic publication or upstream integration occurs.

## Licence

[GNU GPL v3 or later](LICENSE), as stated in [COPYRIGHT](COPYRIGHT). Dependency licences remain in [third-party notices](THIRD_PARTY_NOTICES.md). Binary downloads include matching dependency-inclusive source archives.
