# Agent context in lanes

A lane is built from a Git commit, so it contains what is committed. The files that guide an agent are often not committed: `.kiro/steering`, `.claude/agents`, a personal `CLAUDE.md`, prompts, skills, notes. Without them an agent in a lane works without the project's rules.

Workcopy gives every lane a private copy of those instruction files, chosen so the lane stays lean and nothing sensitive travels with it.

## What is copied

Text that guides an agent, from a catalogue of known tools plus any paths you add:

| Tool | Copied from the main checkout |
| --- | --- |
| Generic (always) | `AGENTS.md`, `AGENTS.override.md`, `AGENT.md`, `CONVENTIONS.md` |
| Kiro | `.kiro/steering`, `specs`, `agents`, `hooks`, `skills`, `prompts`, `powers`, `scripts` |
| Claude Code | `CLAUDE.md`, `CLAUDE.local.md`, `.claude/CLAUDE.md`, `agents`, `commands`, `skills`, `rules`, `hooks`, `output-styles` |
| Codex | `.codex/AGENTS.md`, `prompts`, `skills`, `agents` |
| Gemini CLI | `GEMINI.md`, `.gemini/GEMINI.md`, `commands`, `agents` |
| Antigravity | `.agent/rules`, `workflows`, `skills` |
| Cursor | `.cursorrules`, `.cursor/rules`, `commands` |
| Windsurf | `.windsurfrules`, `.windsurf/rules`, `workflows`, `skills` |
| Cline, Roo Code, Kilo Code | `.clinerules`; `.roorules`, `.roo/rules`; `.kilocode/rules` |
| Continue, Copilot, Amazon Q, Junie, Augment, Trae, OpenHands, OpenCode | their rules, instructions, prompts and agents directories |
| Crush, Warp, Goose, Zed, Replit | `CRUSH.md`; `WARP.md`; `.goosehints`; `.rules`; `replit.md` |

`workcopy agent-context` prints the exact list for your repository. The catalogue reflects conventions known in November 2026; a tool it does not know is reported as a *candidate*, never copied silently.

## Which agent a lane serves

A lane does not need every tool's files. A Kiro session wants `.kiro/steering`, not `.cursor/rules`. `WORKCOPY_AGENTS` chooses, and the tool-neutral files (`AGENTS.md` and kin) are always included:

| Value | Meaning |
| --- | --- |
| `auto` (default) | Over MCP, the client that is calling decides (below). Over the CLI, or when the client is not recognised, every known tool is included, because guessing wrong would leave an agent without its rules. |
| `all` | Every tool in the catalogue. |
| tool keys | `kiro`, `claude`, `codex` (also OpenAI and ChatGPT), `gemini`, `antigravity`, `cursor`, `windsurf`, `cline`, `roo`, `kilo`, `continue`, `copilot`, `amazonq`, `junie`, `augment`, `trae`, `openhands`, `opencode`, `crush`, `warp`, `goose`, `zed`, `replit`; separate several with `;` or `,`. `auto;kiro` means "the calling client, plus Kiro". |

Per lane: `workcopy create NAME --agent kiro,codex`, or the `agents` argument of `workcopy_create` over MCP. The choice is recorded in the lane, so `sync` and `agent-context --refresh` keep serving the same tools; `agent-context NAME --refresh --agent claude` (MCP: `workcopy_refresh_context` with `agents`) moves a lane to another tool, removing untouched copies of the old one. A lane created before this existed keeps every tool. The report says which tools a lane serves and why, and lists known tools that have files in the checkout but are not served, so a missing rule file is explained.

Precedence: an explicit request for the lane, then the owner's `WORKCOPY_AGENTS`, then (for `auto`) the calling MCP client.

### How the MCP client is recognised

An MCP client names itself in its `initialize` request (`clientInfo.name`). Workcopy matches that name case-insensitively against fragments for each tool, so version suffixes do not matter: `claude-code` and `Claude Desktop` select `claude`; `codex-mcp-client`, `openai` and `chatgpt` select `codex`; `gemini-cli-mcp-client` selects `gemini`; `cursor-vscode` selects `cursor`; `Visual Studio Code` and `copilot` select `copilot`; `Roo Code` selects `roo`; `Cline`, `opencode`, `kiro` (and `Kiro CLI`), `windsurf`, `antigravity`, `junie`, `goose`, `zed` and the rest by name. Short names must begin a word, so `optimized-agent` does not match `zed`. Of these names, Gemini CLI and Roo Code were read from their source; the others are the forms the products document or report, so a client that announces something unexpected falls back to including every tool and can always be told explicitly with `agents`. The name a client sent is shown as `client` in `workcopy_bootstrap` and `workcopy_agent_context`.

The calling process is not inspected. A server cannot reliably tell which program started it (a Windows editor launching `wsl.exe`, a wrapper script, a container), whereas the protocol's own client name is exact and portable.

## What is never copied

Safety filters that no setting can switch off:

- **Private keys and credential stores:** `*.pem`, `*.key`, `*.pfx`, `*.p12`, keystores, `id_rsa` and kin, `.npmrc`, `.pypirc`, `.netrc`, and anything inside `.ssh`, `.aws`, `.gnupg`, `.azure` or `.kube`. Not even the opt-ins below reach these.
- **Credentials and local settings, unless you opt a file in:** `.env*`, `credentials*`, `*secret*`, `*.token`, `auth.json`, `*.local.json`, MCP configuration (`mcp*.json`), each tool's `settings` files. Tool settings are not in the catalogue at all.
- **Content that looks like a credential** (unless the file is named in the secrets list below): private-key blocks, cloud and Git host tokens, JWTs, `password = <long value>`. Placeholders such as `YOUR_API_KEY_HERE` or `${TOKEN}` are fine. The refusal says which line, never the value.
- **Media, archives, binaries, data stores, documents:** images, audio and video, zip/tar, executables, `.sqlite`, `.pdf` and similar.
- **State and history:** anything inside `sessions`, `history`, `cache`, `logs`, `projects`, `todos`, `checkpoints`, `node_modules`, build output.
- **Anything that is not text**, symbolic links (never followed), files over the size limit, and everything past the file-count or total-size limit (reported, not truncated).
- **Tracked files:** the lane already has them from Git.

## Tool configuration your agents need (opt-in)

Some agents cannot work without a tool's configuration, for example an MCP server definition for your own tools, or a settings file that lives beside the project and that the agent's sandbox cannot reach from your home directory. Those files are refused by default, but you can opt specific files in. This is owner-only: it comes from your settings file, environment or flags, never from the repository's own configuration and never from an MCP tool argument, so an agent cannot widen what is copied.

```ini
# configuration files to copy into every lane (patterns need a literal first directory)
WORKCOPY_AGENT_CONTEXT_CONFIG=.kiro/settings/mcp.json;.mytool/*.toml
# exact files you accept may hold credentials, copied without the credential scan
WORKCOPY_AGENT_CONTEXT_SECRETS=.mytool/.env
```

- `WORKCOPY_AGENT_CONTEXT_CONFIG` copies settings and MCP definition files (`.json`, `.toml`, `.yaml`, `.ini`, `.cfg`, `.conf` and similar). A file whose name or content looks like a credential is still refused, with the reason, and the reason names `WORKCOPY_AGENT_CONTEXT_SECRETS` as the way to override it for that one file.
- `WORKCOPY_AGENT_CONTEXT_SECRETS` names **exact files** (wildcards are rejected). Use it when a tool's config holds a token your agent needs. The copy is marked `sensitive` in every report and in the lane's manifest, and the content is not scanned. The file is still bounded by the size limits and must be text.
- Both accept only paths inside the repository. Keys, keystores and credential stores stay refused whatever you list. The copy lives in the lane directory, readable by anything that can read that directory, so only list a file you are willing to hand the agent.
- These copies obey the same contract as instruction files: not in `status`, reviews or commits, discarded by `destroy` without `--force`, refreshed on `sync`, an edited copy kept. Configuration outside the repository (for example under your home directory) is not copied; point the tool at it with the tool's own settings, or keep a copy inside the repository's untracked area.

## They are not source

A copy lives in the lane but is not part of its work:

- it does not appear in `status`, in a review or in a commit, and cannot be promoted;
- `destroy` discards copies without `--force`, and says so in the preview (including how many were edited in the lane, because those edits are lost);
- an agent that edits its copy changes only that lane; the main checkout is never written.

## Staying in sync with the main checkout

- A new lane takes the current files.
- `workcopy agent-context NAME --refresh` (MCP: `workcopy_refresh_context`) updates an existing lane: new files are added, changed files are replaced, files deleted in the main checkout are removed.
- `sync` does this automatically (`WORKCOPY_AGENT_CONTEXT_REFRESH_ON_SYNC=off` stops it).
- A copy that was **edited in the lane**, or **deleted** there, is left alone and reported. A different local file at the same path is never overwritten.

## Configure it once

Everything is in the [settings file](settings.md), so one change applies to every later lane. A person or an agent edits the same file:

```ini
# serve particular agents (otherwise the calling MCP client decides)
WORKCOPY_AGENTS=kiro,codex
# add a tool Workcopy does not know, or a project folder of agent notes
WORKCOPY_AGENT_CONTEXT_INCLUDE=.newtool/rules/**;docs/agents/**
# keep something out
WORKCOPY_AGENT_CONTEXT_EXCLUDE=.kiro/specs/**
# or tune the limits / switch it off
WORKCOPY_AGENT_CONTEXT_MAX_FILE_KIB=128
WORKCOPY_AGENT_CONTEXT=off
```

`workcopy agent-context` shows what a new lane would receive, what was refused and why, the candidates, and the settings file to edit. The MCP server shows the same through `workcopy_bootstrap` (a short summary) and `workcopy_agent_context` (the full plan), and its guide tells agents what to do when something that should guide them is missing: ask the owner to add the path to `WORKCOPY_AGENT_CONTEXT_INCLUDE`. Agents cannot change the settings through MCP, and no tool argument can copy configuration.

Dependency folders and other large directories are not agent context; use `share` for those.

## Limits and edge cases

- Defaults: 256 KiB per file, 8 MiB and 400 files per lane.
- If a copied path later becomes tracked upstream, the next `sync` reports a conflict for it; resolve it with `--use-upstream`.
- Copies are plain files with no Git history. A lane agent that needs a newer version runs a refresh.
