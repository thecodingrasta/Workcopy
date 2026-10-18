# Workcopy

**Give every developer and AI agent their own editable copy of the code. No Git risk. No shared working directory.**

[![Native CI](https://github.com/thecodingrasta/workcopy/actions/workflows/ci.yml/badge.svg)](https://github.com/thecodingrasta/workcopy/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/thecodingrasta/workcopy?include_prereleases)](https://github.com/thecodingrasta/workcopy/releases)
[![Licence: GPL v3 or later](https://img.shields.io/badge/licence-GPLv3%2B-blue.svg)](LICENSE)
[![Go 1.25+](https://img.shields.io/badge/source-Go%201.25%2B-00ADD8.svg)](go.mod)
[![MCP](https://img.shields.io/badge/MCP-stdio-6750A4.svg)](docs/mcp.md)

Workcopy copies committed source into a plain folder called a **lane**. No `.git` inside it, so nothing can reset a branch, corrupt an index or push anywhere. Edit, build and test there. When you're done, Workcopy prepares a separate, unstaged review for a human to check before anything is integrated. Your real checkout is never touched.

## Why it exists

Letting an agent run loose in your actual repository is risky: one bad `git reset` or branch switch and you've got a mess. A full `git clone` per worker is safe but wasteful on a big monorepo, and still gives every worker real Git access to misuse.

Workcopy sits in between. It creates a lane from the last commit, not your uncommitted edits, copies only the projects a task needs, and hands back a Git-free folder. Work happens there. Reconciliation against upstream changes and the final review are separate steps you control.

## Workcopy vs. `git worktree`

| | `git worktree` | Workcopy lane |
| --- | --- | --- |
| Contains `.git` | Yes, full Git checkout | No, never |
| Branch locking | Can't check out a branch already in use elsewhere | None, lanes aren't checkouts |
| What gets copied | Entire working tree for the branch | Only the projects you select |
| Review step | None built in | Separate unstaged review, prepared automatically |
| Agent risk | Can still run destructive Git commands | No Git commands available at all |
| Best for | One trusted developer, one extra branch | Several workers, human or AI, on one codebase |

Both tools write a full copy of selected files to disk, and both duplicate build output (`bin`, `obj`, `node_modules`) per working copy. Workcopy doesn't deduplicate that either. The real difference is that a worktree is still Git; a lane simply isn't, so there's nothing for a runaway agent to break.

What actually keeps disk use down with Workcopy is **project selection**: on a monorepo, a lane only copies the projects a task needs, not the whole tree. Build output and package caches are not deduplicated by either tool.

## Install

Needs Git 2.43 or newer. Go is only needed to build from source.

1. Download your platform's archive and `SHA256SUMS` from [Releases](https://github.com/thecodingrasta/workcopy/releases).
2. Verify the checksum, unzip, and put `workcopy` (or `workcopy.exe`) on your `PATH`.
3. Run `workcopy version` then `workcopy doctor` to confirm it works.

Before the first published release, use the [Windows amd64 candidate](prebuilt/README.md). Other platforms are available as [CI artifacts](https://github.com/thecodingrasta/workcopy/actions/workflows/ci.yml) (GitHub sign-in required).

Building from source needs at least Go 1.25:

```sh
go run ./tools/dev setup
go run ./tools/dev build
```

The executable lands in `dist/`. `go run ./tools/dev install` copies it to `GOBIN`/`GOPATH/bin`; neither command touches your `PATH`.

## Use Workcopy with an AI agent (MCP)

The same executable runs as a local MCP server over stdio. No network listener, no separate install.

Codex, Claude Code, Kiro, Antigravity and most other MCP hosts all take the same `mcpServers` JSON block, just in different config files:

```json
{
  "mcpServers": {
    "workcopy": {
      "command": "C:\\tools\\workcopy.exe",
      "args": ["mcp", "--root", "C:\\workcopy-lanes", "--max-lanes", "32", "--reserve-mib", "8192"]
    }
  }
}
```

Replace the executable path and `--root` (a private folder outside any Git checkout). On Linux/macOS use a native absolute path, e.g. `/opt/workcopy/workcopy`. One server handles multiple repositories, so no `--repo` is needed here.

| Host | Config file |
| --- | --- |
| Codex | `~/.codex/config.toml` ([TOML format](docs/mcp.md#codex), not JSON) |
| Claude Code | project `.mcp.json` or global settings |
| Kiro | `.kiro/settings/mcp.json` or user settings |
| Antigravity | its MCP server settings |
| Other hosts | wherever that host reads `mcpServers` JSON |

Restart the host after saving, then tell your agent:

> **"Use Workcopy MCP for this repository. Create a lane for my task, validate inside it and prepare a review for me."**

The agent discovers projects and previews each lane before creating it. It needs your approval for any dependency download; Workcopy itself never installs packages. You still review every result before integration. Full details, every tool and multi-repository setup: [MCP guide](docs/mcp.md). Multi-agent ownership and handoffs: [controller and worker prompts](docs/agents.md).

## Your first lane

Run inside an existing Git repository with at least one commit:

```sh
workcopy doctor
workcopy create fix-parser --all
workcopy status fix-parser
```

`status` prints the lane's folder path. Open it and edit normally. Lane creation only copies **committed** source, so commit anything you need first. Use `--root` to store the lane in a private folder outside any Git checkout.

Inside the lane:

```sh
workcopy status
workcopy sync
workcopy merge
```

`sync` catches you up with upstream changes and flags conflicts to fix before re-running `merge`. `merge` doesn't commit anything; it prints the path to a separate review folder. Inspect that diff yourself before deciding whether to bring it in. See the [workflow guide](docs/workflow.md) for approval, recovery and cleanup.

## Large repositories

Select only the projects a task needs instead of copying everything:

```sh
workcopy discover
workcopy create api-fix --project src/Api --project src/Api.Tests --dry-run
workcopy create api-fix --project src/Api --project src/Api.Tests
```

Use the project IDs or paths `discover` reports. Check the dry-run's selected files and warnings before creating the lane for real. For repeatable selections, commit a `.workcopy.json` profile and use `--profile`; see the [configuration guide](docs/configuration.md) and [a 25-project example](examples/monorepo.workcopy.json).

Build and test inside the lane. Workcopy doesn't run scripts or install packages. Keep `bin`, `obj`, `node_modules` and other build output lane-local; never point two lanes at the same output directory. Package manager caches usually already live outside lanes and get reused automatically. See [storage](docs/storage.md) for the full picture.

## Safety and support

- Lanes are workflow boundaries, not security sandboxes. Use host/OS permissions when real isolation matters.
- `destroy` protects uncommitted contributions and conflicts. `--force` discards your own changes, never shared targets.
- Local filesystems only. Network shares and cloud-synced folders aren't supported.
- Git filters, LFS, submodules, files over 64 MiB, and repos over 100,000 tracked paths are rejected explicitly.

See [validation status](docs/validation.md) for what's been tested natively versus compiled only. Read [SECURITY.md](SECURITY.md), [troubleshooting](docs/troubleshooting.md) and [SUPPORT.md](SUPPORT.md) before filing an issue.

## Contribute

Bug reports, platform validation and doc fixes welcome. Start with [CONTRIBUTING.md](CONTRIBUTING.md). CI runs formatting, vet, race tests, CLI/MCP workflows and a vulnerability scan. Maintainers review and publish releases; nothing publishes automatically.

## Licence

[GNU GPL v3 or later](LICENSE), see [COPYRIGHT](COPYRIGHT). Dependency licences in [third-party notices](THIRD_PARTY_NOTICES.md). Binary downloads include matching source.
