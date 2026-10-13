# Repository instructions for coding agents

Read `README.md`, `docs/architecture.md` and `CONTRIBUTING.md` before changing behaviour. Use the existing package boundaries. Do not create a second workspace engine or replace the CLI framework without a demonstrated need.

Reproduce defects with real disposable Git repositories. Add a failing regression before a fix. Run `go run ./tools/dev validate` and report exact commands, results and unavailable platforms. Cross-compilation is not native validation.

Never exercise destructive tests against a user's real lanes. Do not weaken ownership checks, skip recovery preconditions, remove journals, hide unresolved conflicts or mark resolved local bytes as upstream baseline. Preserve main HEAD, symbolic HEAD, index and human edits through Workcopy operations.

No automated push, public release, credentials, dependency install, global Git setting or machine configuration change is implied by a coding task. Release publication requires maintainer approval. Keep internal notes and handover material out of the public tree.
