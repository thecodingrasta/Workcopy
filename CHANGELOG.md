# Changelog

## Unreleased

- MCP supports explicit repository/root scope per call, repository-free startup and repository-specific default storage directories. Existing startup defaults remain compatible.
- Checkout identity uses filesystem identity rather than Unicode case folding, preventing distinct Windows repositories from inheriting startup configuration or storage.
- Integration gate package deadlines are bounded at three minutes to accommodate the expanded native workflows. MCP operation deadlines remain two minutes.
- Disposable fixture discovery supports trimmed-path test builds, allowing validation to reuse compiler artifacts without duplicating path variants.
- Formatting checks and writes exclude vendored dependencies and private cache/runtime directories; clean source bundles validate without rewriting third-party code.
- Directory bootstrap identifies an existing Git-free lane; low-space guarded cleanup and previews remain available without weakening ownership or allocation checks.
- Repeated expansion retains no-change semantics below the reserve; invalid resolution-file arguments return usage error 2.
- Download-first onboarding includes copy-ready Codex and JSON MCP settings, an initial agent task and explicit scope handoffs.
- GNU GPL v3 or later licensing, with original third-party notices preserved.
- Windows bootstrap candidate, checksums and dependency-inclusive corresponding source; native packaging and reviewed release drafts.
- Public installation/MCP guides, live CI/release badges, native ecosystem validation and honest platform coverage.
- Private agent metadata, caches and generated output excluded from Git; editor and line-ending settings included.
- .NET lane acceptance disables persistent build/compiler servers so Windows fixture cleanup does not retain locked analyzer files.
- The contributor runner accepts LF and CRLF module declarations; module files have explicit LF checkout attributes.
- Windows junction creation normalizes local drive path separators, including forward-slash temporary directories.

- Stdio MCP using the official SDK, current/legacy protocol support, typed tools, bootstrap context, operating prompts and human review handoffs.
- MCP storage admission, bounded collection/content retrieval, allowlisted explicit sharing and lane-confined resolution files.
- Owner-selected MCP startup configuration supports explicit catalogs and profiles without exposing config-path overrides to agents.
- MCP creation rejects a source ref that advances between admission and materialization; owner configuration contents remain pinned for the server lifetime.
- External .NET dependency warnings permit independent discovery; expansion previews report source bytes.
- Selecting a known project by its directory enforces the same dependency closure as selecting its ID.
- Source builds require Go 1.25 or newer; MCP dependencies are pinned in `go.mod`/`go.sum`.
- Windows support dependency includes the fix for GO-2026-5024; optional offline Node workspace acceptance verifies lane-local build and test scripts.

- Native Go CLI for snapshot-based Git-free lanes, selected project profiles and incremental expansion.
- Three-way reconciliation with persistent conflict tokens and hash-labelled content reads.
- Unstaged review worktrees and explicitly approved detached review commits.
- Local dependency sharing through Unix symlinks or Windows junctions, with input-drift warnings.
- Journal recovery, writer locks and guarded deletion that retains dependency targets.
- Declarative project discovery, .NET solution filters, structured JSON and local diagnostics.
- Disposable real-Git integration tests, command-line acceptance tests and native CI jobs.
- Agent guidance for concurrent lanes, shared root-file ownership, and the limits of lane isolation.
- Create help now advertises `--dry-run`; command and troubleshooting docs explain repeat-preflight name collisions and sandbox permission failures.

This entry describes available source functionality. A successful native CI run and the release checklist are required before assigning a stable release tag.
