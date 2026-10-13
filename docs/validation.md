# Validation status

This page records local evidence and the checks contributors can repeat. The CI badge links to actual workflow results; it does not certify a release before a successful run.

## Local evidence

| Check | Evidence |
| --- | --- |
| Windows amd64 / local NTFS | Native CLI, MCP, real-Git fixtures, dirty-checkout preservation, reconciliation, recovery and guarded cleanup tested |
| Three concurrent lanes | Offline Go validation, deliberate conflict, explicit resolution and separate unstaged reviews tested |
| Multiple repositories through one MCP | Three same-named lanes, per-call root override, current-directory bootstrap, concurrent offline Go tests and separate review receipts tested |
| Windows checkout identity | Distinct Unicode checkout paths retain separate configuration and default storage; regression reproduced before correction |
| Go, Node and .NET | Small selected-project fixtures built and tested in lanes; this does not certify arbitrary repositories |
| Linux/macOS | Native execution unavailable on the Windows development host; CI and contributors must qualify these platforms |
| Other architectures | Cross-compilation only unless a native runner explicitly executes the checks |
| Case-sensitive filename collision fixture | Cannot run on the case-insensitive NTFS volume; native case-sensitive coverage required |

The clean-source Windows gate passed with Go 1.25.6, Git 2.45.1.windows.1 and MinGW-w64 GCC 15.2.0 with `libsynchronization.a` available. It included formatting, vet, race tests, the offline Node lane fixture, a native build and JSON CLI smoke checks. The selected .NET lane restore/build/test passed with SDK 10.0.401 and .NET 8 runtime 8.0.31. The Windows candidate reports Go 1.27.2; `govulncheck` v1.1.4 found no vulnerabilities with that toolchain. A separate scan with the installed Go 1.25.6 reported 27 reachable standard-library advisories, so that outdated patch must not be used for release builds. CI's `1.25.x` job selects the current patch. A local pass does not imply a newly edited workflow has run on GitHub.

Repeat the native gate with both lane fixtures enabled:

```powershell
$env:WORKCOPY_DOTNET_LANE_ACCEPTANCE = '1'
$env:WORKCOPY_NODE_LANE_ACCEPTANCE = '1'
go run ./tools/dev validate
```

The .NET fixture restores pinned test packages into disposable storage. Obtain approval for network access before running it locally. Node runs tiny offline fixture scripts without installation. GitHub CI enables these fixtures explicitly.

## CI gates

`.github/workflows/ci.yml` runs:

- Go 1.25 API floor and current stable Go on Windows, Linux and macOS: formatting, vet, race tests, real CLI/MCP journeys, build and diagnostics.
- Explicit selected .NET restore/build/test and offline Node script fixtures on all three operating systems.
- Current vulnerability database scanning. Scanner/network errors fail the job.
- Native candidate archives and matching source, uploaded as temporary artifacts after native checks.

Fixture restores in CI download only the fixture's pinned packages. Workcopy itself never restores packages or executes arbitrary build scripts. CI runner architectures can change; use each run's `go env`/runner evidence rather than infer architecture from an OS label.

## Release qualification

The manual draft-release workflow validates the reviewed tag, builds six OS/architecture combinations, smoke-tests the native runner assets and attaches checksums plus dependency-inclusive source. Targets without native execution remain compile-only. A maintainer reviews results and publishes the draft.

No laptop check can prove Linux/macOS runtime behaviour, future GitHub availability or every consumer project's build inputs. Contributions with a minimal disposable fixture and exact platform/tool versions are welcome. Follow [CONTRIBUTING.md](../CONTRIBUTING.md) and [release procedure](releasing.md).
