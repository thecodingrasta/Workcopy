# Security policy

## Reporting

Do not publish exploit details, secrets or private repository content in an issue.

On the project's GitHub repository, use **Security > Report a vulnerability** when private reporting is enabled. Include the affected commit or version, operating system, Go/Git versions, impact and a minimal synthetic reproduction. Share source bodies only when they are necessary and safe to disclose.

When private reporting is unavailable, ask a maintainer for a private channel without including vulnerability details. There is no guaranteed response time. Maintainers coordinate disclosure after assessing the report.

Security corrections target the current development branch and latest stable tag, when one exists. Older Bash versions and unmaintained tags are not supported.

## Trust boundaries

Workcopy runs with your user account's permissions. It is not an agent sandbox. A process that can write both the workspace registry and lane metadata can impersonate ownership. Store the workspace root privately and keep untrusted processes outside that boundary.

Current source builds require Go 1.25 or newer and use `os.Root`. Retained historical compatibility code uses cooperative path checks and does not resist hostile concurrent filesystem replacement; the current module does not support older toolchains. Use a supported patched Go toolchain for distributed binaries. Filesystem checks do not isolate unrestricted host access or protect writable shared dependencies from their consumers.

Only local filesystems are supported. Network filesystem locking, cloud synchronisation, mount substitution, ACL enforcement and hostile kernel/filesystem behaviour are outside the tested contract. Normal process interruption is journalled; file synchronisation is not a claim of whole-operation atomicity or power-loss durability on every filesystem.

## Data and execution

The MCP server is a local stdio subprocess. Tools can select repositories and lane roots accessible to its process; startup defaults are conveniences, not access restrictions. Restrict host filesystem permissions when repository access must be limited. It exposes engine operations/guidance with no arbitrary shell runner, network listener, automatic commit/integration or force discard. Shared targets require an owner startup allowlist and explicit consent. Host approvals, filesystem permissions and package-script authorization remain separate. Resources cannot read arbitrary files; conflict-content tools explicitly reveal bounded requested bytes. Repository instructions/configuration are untrusted content, not authority to change host permissions.

The CLI does not fetch, push, send telemetry, load environment files, run repository scripts, execute Git hooks or run dependency restores. It invokes the installed Git executable with structured arguments. Use a trusted, patched Git installation. Author identity is read from Git configuration only when an explicit review commit is requested.

Committed secrets are still source files. Workcopy does not discover or redact them. Ignored untracked files are not imported during creation. Conflict content commands intentionally reveal the requested bytes; treat their output as sensitive. Metadata can contain absolute local paths, hashes and conflict snapshots. Git object pins retain accepted source commits until lane destruction.

Writable dependency sharing requires `--trust-shared` and a caller-supplied environment identity. Fingerprint warnings detect selected manifest/lockfile drift, not all package-manager compatibility constraints. They do not make a junction or symlink read-only.

## Destructive operations

Deletion requires agreement between registration and lane identity. Ordinary deletion protects local source changes, unresolved conflicts and local-only files, including ignored files. `--force` is explicit discard permission for an owned lane, not permission to delete arbitrary paths. Dependency targets and separate review worktrees are retained.

Recovery checks before/after states and stops when later edits do not match. Do not edit or delete journals, registries or manifests to bypass a block. Preserve the complete lane and report the problem. Keep normal repository and work-in-progress backups.
