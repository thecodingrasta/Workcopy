# Architecture

## Boundaries

`cmd/workcopy` is the process entry point. `internal/cli` validates arguments and formats results. `internal/workspace` coordinates lifecycle, selection, reconciliation and review. `internal/git` owns structured Git process calls, snapshot reads and isolated indexes. `internal/fsops` owns path policy and native filesystem operations. `internal/state` owns snapshots, writer locks, content objects and recovery. `internal/project` reads declarative project boundaries.

The CLI is not the application model. `internal/mcpserver` adapts the same engine operations to the official Go MCP SDK over local stdio. Each call selects its repository/root without changing shared defaults. A repository-free server namespaces its default storage parent by repository identity; explicit roots use the existing engine ownership rules. Typed tools expose bounded collections, lane/build context, conflict content and human review preparation; prompts/resources establish the controller/worker contract. Storage admission complements locks/ownership guards; it is not a build-output quota or sandbox. There is no arbitrary command runner, automatic integration, force-discard tool, network listener, GUI or external plugin runtime.

## Stored state

Each lane stores one versioned snapshot at `.workcopy/manifest.json`. It contains identity, source repository, resolved selection, accepted baseline versions, conflicts, mappings and review receipts. A separate registration under the workspace root ties the lane name and absolute path to the same repository and identity.

Baseline versions describe file kind, executable mode, content hash, size and Git object ID. Non-Git content is staged in a content-object store with a 64 MiB per-object limit. Expansion and synchronization retain file versions rather than all payloads in memory. Successful transactions collect unreferenced objects; interrupted planning can retain staging until the next successful mutation. There is no global storage quota. Accepted source commits are pinned under `refs/workcopy/` so ordinary Git garbage collection does not discard a required baseline.

A mutation records its expected generation and before/after file versions in a journal. Apply validates all preconditions, replaces individual files independently and writes one new state snapshot. Recovery accepts a recorded before state or an already applied after state, never an unrelated later edit. Creation, review and deletion have registry/receipt phases for operations spanning more than one journal. File/directory transitions delete only separately authorized files and then empty directories. Unplanned or newly written files block replacement.

This is recoverability, not a claim that a multi-file operation is one atomic filesystem transaction. Workcopy serialises its own writers. Editors must not concurrently modify the same files during apply.

## Reconciliation

Let B be the accepted upstream version, L the local lane version and U the new upstream version. Resolution chooses R, then stores U as the accepted baseline and R as local content. The difference U to R remains promotable. The baseline must never be replaced by R merely to make status clean.

Conflicts carry their own B/L/U provenance and identity. Resolving one path cannot clear another conflict or pretend the entire upstream snapshot is accepted. The global synchronized commit advances only when the selected content has been reconciled.

## Review

A review is a separate full Git worktree with detached HEAD. Workcopy materializes canonical blobs and local contributions, then initializes the baseline index without staging those contributions. The original checkout's index is never used for lane scanning or review commits.

Explicit approval validates the saved receipt, lane generation, files, index and detached HEAD. A temporary index produces the reviewed tree. A compare-and-swap update advances only the detached review HEAD. The receipt distinguishes preparing, prepared, committing and committed states. It does not claim upstream integration.

## Filesystem policy

Editable source uses independent file replacement, not hardlinks. Source symlinks must stay within the lane. Managed shared directories require explicit consent and have separate lifecycle checks. Deletion inventories owned entries, quarantines the lane and unlinks entries without descending through shared links.

Current source builds require Go 1.25 or newer and use `os.Root`. Historical compatibility code has cooperative path validation and is not a supported older-toolchain build of the current module. Windows uses native file locks and junction creation; Linux/macOS use advisory file locks and symlinks. Platform implementations share governance semantics, not identical operating-system APIs. See [SECURITY.md](../SECURITY.md) for the threat boundary.
