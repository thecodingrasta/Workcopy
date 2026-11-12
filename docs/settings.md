# Settings

Every preference you would otherwise repeat on a command line lives in one place: a settings file named `workcopy.env`. Set a value once and the CLI and the MCP server both use it.

```sh
workcopy settings --init      # writes a commented template, never overwrites
workcopy settings             # every effective value and where it came from
```

## Where the file lives

Workcopy reads the first of these that applies:

1. `--settings FILE` on the command line.
2. The `WORKCOPY_SETTINGS` environment variable.
3. The per-user file: `%APPDATA%\Workcopy\workcopy.env` on Windows, `~/Library/Application Support/Workcopy/workcopy.env` on macOS, `$XDG_CONFIG_HOME/workcopy/workcopy.env` (normally `~/.config/workcopy/workcopy.env`) on Linux.

No per-user file is normal on a first run. A file you name explicitly that does not exist is an error, so a typo in a path is never silent. Under WSL the Windows build and the Linux build have separate per-user locations; point both at one file with `WORKCOPY_SETTINGS` (give the Windows build a Windows path, the Linux build a `/mnt/c/...` path).

## Precedence

A command-line flag beats an environment variable, which beats the repository's own file (below), which beats your user file, which beats the built-in default. Every setting can be given in any of the three places using the same `WORKCOPY_` name. `workcopy settings` shows the winner for each key and its source.

## File format

`KEY=VALUE` per line, `#` for comments, optional `export `, single or double quotes for values with spaces. Keys that do not start with `WORKCOPY_` are ignored so the file can be shared with other tools. An unknown `WORKCOPY_` key, a value outside its range or a malformed line stops the command with the line or key named: a typo must not silently do nothing. List settings accept `;` between values or the key repeated on several lines.

## Settings

| Key | Default | Meaning |
| --- | --- | --- |
| `WORKCOPY_REPO` | none | Default checkout when `--repo` is not given and the working directory is not inside a lane |
| `WORKCOPY_ROOT` | beside the checkout | Where lanes are stored |
| `WORKCOPY_CONFIG` | none | Owner configuration file (projects, profiles, `destroy.discard`); treated as owner-supplied |
| `WORKCOPY_MAX_LANES` | 32 | MCP: lanes per repository and root; 0 disables the limit |
| `WORKCOPY_RESERVE_MIB` | 2048 | MCP: free disk space to keep in reserve (at least 2048) |
| `WORKCOPY_TOOL_TIMEOUT` | 2m | MCP: deadline for one tool call, 10s to 24h (`90s`, `15m`, `2h`) |
| `WORKCOPY_ALLOW_SHARE_TARGET` | none | MCP: directories an agent may link with `share` |
| `WORKCOPY_GIT` | first `git` on `PATH` | Git program to run; a value that does not resolve fails rather than falling back |
| `WORKCOPY_IO_WORKERS` | 2× CPUs, 4 to 16 | Concurrent file operations; `1` is strictly sequential |
| `WORKCOPY_MAX_FILE_MIB` | 2048 | Largest tracked file Workcopy copies into a lane, in MiB (64 to 1,048,576). Files over 64 MiB are streamed and compared by hash, never held in memory, never converted for line endings and never text-merged: if both sides changed one, `sync` reports a conflict. A file over the limit is refused with its path and size |
| `WORKCOPY_PATH_STYLE` | auto | How paths are written in answers: `auto` (the spelling you passed), `native`, `wsl` or `windows`. Flag: `--path-style`. See [paths](wsl.md#path-forms) |
| `WORKCOPY_WSL_MOUNT_ROOT` | `/mnt` | Windows build only: the directory where WSL mounts Windows drives when `/etc/wsl.conf` moved it (`/` for drives at `/c`, `/d`). It decides which paths the build reads as drive paths and how it writes them |
| `WORKCOPY_JSON` | off | CLI: print JSON by default |
| `WORKCOPY_AGENTS` | auto | Which agent tools a lane serves: `auto` (the calling MCP client decides; everything when unknown), `all`, or keys such as `kiro,codex,claude,gemini,opencode` ([details](agent-context.md#which-agent-a-lane-serves)). Flag: `--agent` |
| `WORKCOPY_AGENT_CONTEXT` | on | Copy agent instruction files into lanes ([details](agent-context.md)) |
| `WORKCOPY_AGENT_CONTEXT_DETECT` | on | Recognise the built-in catalogue of agent tools |
| `WORKCOPY_AGENT_CONTEXT_INCLUDE` | none | Extra repository-relative globs to copy |
| `WORKCOPY_AGENT_CONTEXT_EXCLUDE` | none | Globs never to copy |
| `WORKCOPY_AGENT_CONTEXT_CONFIG` | none | Owner-only opt-in: tool configuration files (MCP definitions, tool settings) to copy into every lane; credentials stay refused ([details](agent-context.md#tool-configuration-your-agents-need-opt-in)) |
| `WORKCOPY_AGENT_CONTEXT_SECRETS` | none | Owner-only opt-in: exact files you accept may hold credentials, copied without the credential scan and marked sensitive; key files and cloud credential stores stay refused |
| `WORKCOPY_AGENT_CONTEXT_MAX_FILE_KIB` | 256 | Largest single file |
| `WORKCOPY_AGENT_CONTEXT_MAX_TOTAL_KIB` | 8192 | Total per lane |
| `WORKCOPY_AGENT_CONTEXT_MAX_FILES` | 400 | File count per lane |
| `WORKCOPY_AGENT_CONTEXT_REFRESH_ON_SYNC` | on | Refresh unmodified copies whenever a lane syncs |

Flags map to keys where one exists: `--root`, `--repo`, `--config`, `--max-lanes`, `--reserve-mib`, `--tool-timeout`, `--allow-share-target`, `--agent`, `--json`. `WORKCOPY_AGENTS` also accepts commas, which survive an unquoted shell command line where `;` does not.

## One file per repository

Lane location and owner configuration belong to a project, not to a user. Each repository can have its own settings file in its Git directory:

```sh
cd path/to/repo
workcopy settings --init --project-file     # writes <repo>/.git/workcopy.env, never overwrites
```

```ini
# <repo>/.git/workcopy.env
WORKCOPY_ROOT=D:\lanes\olp
WORKCOPY_CONFIG=D:\olp\owner.workcopy.json
```

With that file, `workcopy create`, `list`, `path` and every other command run from the checkout, from a lane or by `--repo` pick up the repository's root and owner configuration with no wrapper script and no flags, and so does the MCP server for calls scoped to that repository. Another repository is unaffected. `workcopy settings` shows `project` as the source of each value it supplies.

Precedence for these keys: flag, environment variable, the repository file, your user file, default. The MCP server started without `--repo` treats `--root` as the parent directory for each repository, so it does not override a repository's own `WORKCOPY_ROOT`. The repository file may set only `WORKCOPY_ROOT`, `WORKCOPY_CONFIG`, `WORKCOPY_MAX_FILE_MIB`, `WORKCOPY_IO_WORKERS`, `WORKCOPY_AGENTS` and the `WORKCOPY_AGENT_CONTEXT_*` keys, and its paths must be absolute. Anything else (the Git program, share targets, MCP limits) is refused with `invalid-settings-file`, because a program to run or a directory to link is the user's decision. Git does not track its own directory, so the file cannot be committed or arrive with a clone, and it is not copied into lanes.

## MCP hosts

Because the file is read at server start, an MCP host configuration no longer needs arguments for these values:

```json
{ "mcpServers": { "workcopy": { "command": "workcopy", "args": ["mcp"] } } }
```

Changes to the file take effect the next time the host starts the server. The settings file is yours: it is never read from a repository, and an agent cannot change it through the MCP tools.

## Example

```ini
# %APPDATA%\Workcopy\workcopy.env
WORKCOPY_ROOT=D:\workcopy-lanes
WORKCOPY_CONFIG=D:\workcopy\team.workcopy.json
WORKCOPY_TOOL_TIMEOUT=20m
WORKCOPY_MAX_LANES=16
WORKCOPY_ALLOW_SHARE_TARGET=D:\packages-cache
WORKCOPY_AGENT_CONTEXT_INCLUDE=docs/agents/**;.team/rules/**
# serve only the agents you use, or leave on auto so the MCP client decides
WORKCOPY_AGENTS=kiro,codex
# an MCP definition for your own tools, copied into every lane
WORKCOPY_AGENT_CONTEXT_CONFIG=.kiro/settings/mcp.json
```
