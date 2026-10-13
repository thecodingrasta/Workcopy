# Release procedure

Release from a reviewed commit. Keep binary/source checksums tied to that exact packaging input.

## Validate

Run `go run ./tools/dev validate` natively on Windows, Linux and macOS with a supported patched Go release. CI also exercises the Go 1.25 API floor. Record architecture and skipped tests; cross-compilation is not native execution. See [validation status](validation.md) and [compiler prerequisites](../CONTRIBUTING.md#validation-prerequisites).

CI runs formatting, vet, race tests, real CLI/MCP workflows, selected .NET and offline Node fixtures, and vulnerability scanning. Scanner/network failures fail the check. Consumer project builds still require their own inputs and approved dependency policy.

## Package

```sh
go run ./tools/dev clean
go run ./tools/dev package v0.1.0-rc.1
```

Use the actual candidate version. Add `--native` to package only the current OS/architecture. Packaging requires a supported patched Go release, at least Go 1.25.

The task produces six binary archives by default, a matching `_source.tar.gz` archive and `dist/SHA256SUMS`. The source archive includes public source, build scripts, pinned vendored module source and compiler information. Vendoring happens in a private disposable copy; it does not change the repository or global caches. Packaging can download pinned modules when they are absent; use an approved cache for offline builds.

All binary archives retain Workcopy's GPL notice, full licence, third-party notices/licences, Go licence and documentation. Publish corresponding source and checksums alongside binaries. Never substitute a source archive from another commit. The source archive rebuilds with `-mod=vendor`; its `BUILDINFO.txt` records the version and command.

Cross-build success does not certify target runtime behaviour. Extract candidate archives, verify checksums, exercise `version`, `doctor`, help and representative workflows on native hosts. Do not suppress a platform failure or replace a shared link with a copy.

## First push

The reviewed Windows bootstrap candidate under `prebuilt/` is an explicit first-commit exception. It is provisional until the source has a reviewed public tag. Keep its corresponding source and checksums together. Later binaries belong in Releases, not Git history.

The first push to `main` or `master` triggers native CI and temporary native download artifacts. Those expire after 14 days and require GitHub sign-in. Live README badges report actual workflow/release state; before execution or publication they may show no result.

## Draft and publish

Create and push a reviewed version tag yourself. From Actions, select **Draft release**, select that same tag as the workflow ref, and enter its version. The workflow rejects a tag that differs from the checked-out commit, validates natively, packages all targets, verifies extracted binaries on six native runners and opens a draft. Unavailable runners block the workflow. It never creates a tag or publishes a draft automatically.

Versions containing a hyphen create prerelease drafts. Review native results, architecture coverage, vulnerability scan, licence/source bundles, checksums and release notes. Publish the draft yourself only after these checks pass. Durable public downloads appear in [Releases](https://github.com/thecodingrasta/workcopy/releases).

## Repository settings

Enable private vulnerability reporting, protect the default branch, require native validation and review workflow changes. Enable secret scanning where available. Confirm the right to license all source under GNU GPL v3 or later. Local tools do not change remote settings or publish anything.

## Maintain

`go run ./tools/dev clean` removes only recognized owned `dist` outputs. It refuses unknown files and directory links; it never cleans user lanes or shared package caches. Review pinned dependency/action updates and rerun affected protocol, safety and packaging checks.
