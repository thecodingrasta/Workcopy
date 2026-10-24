# Contributing

Start with a bug report, documentation correction or a small testable change. Discuss changes to on-disk state, deletion policy or command semantics before expanding their scope.

## Local setup

Use Git 2.43 or newer and a supported patched Go release, at least Go 1.25. The official MCP SDK and transitive modules are pinned in `go.mod`/`go.sum`. Initial module download needs network access; use an approved cache for offline validation. No Node packages, Python environment or `.env` file are needed.

```sh
go run ./tools/dev setup
go run ./tools/dev test
go run ./tools/dev build
```

The repository also provides `./scripts/dev.sh` and `./scripts/dev.ps1`. Both forward to the same Go task runner. They do not install prerequisites or change machine configuration.

## Make a change

Create a branch in your own clone. Reproduce a defect in a disposable fixture, add a regression test, observe the failure, then make the smallest correction. Keep tests close to the package they exercise. Use `tests/acceptance` for process exit codes, JSON and complete CLI journeys.

Before submitting:

```sh
go run ./tools/dev format
go run ./tools/dev validate
```

Validation checks formatting, runs `go vet`, executes the race-enabled suite, builds the CLI and checks its help, version and diagnostics. A C compiler is required for the race detector. `go run ./tools/dev test` runs the non-race suite, but does not replace full validation for a release.

## Code rules

Use small concrete types, composition and narrow interfaces at actual boundaries. Avoid shell command construction, duplicate path policies, speculative abstractions and dependencies added for convenience. Return actionable errors. Do not log source bodies or credentials by default.

Preserve these invariants: snapshot-correct source, resolved work remains promotable, main checkout isolation, visible conflict state, independent writable source files, read-only dry-runs and ownership-checked deletion. Every new mutation must either be recoverable or fail before changing existing data.

Repository state schemas and JSON exit contracts are public interfaces. Describe compatibility changes and include migration tests. Do not import old Bash metadata automatically or remove a recovery journal to make a failing test pass.

## Evidence in a pull request

State what changed, how to reproduce it, the commands you ran and their actual results. Name the operating system and Go/Git versions. Separate native execution from cross-compilation. Mention skipped or unavailable validation. Do not claim another contributor or agent's reported test result as your own observation.

Do not commit generated output, logs, internal handovers, credentials or private repository fixtures. The reviewed bootstrap candidate under `prebuilt/` is the only binary exception; later binaries belong in Releases. Synthetic identities in tests must use reserved domains such as `example.invalid`.

By contributing, you agree that your contribution is available under [GNU GPL v3 or later](LICENSE), as stated in [COPYRIGHT](COPYRIGHT). You retain your copyright. Follow the [code of conduct](CODE_OF_CONDUCT.md) and report vulnerabilities through [SECURITY.md](SECURITY.md).

## Validation prerequisites

Build and install need Go and Git. Full validation additionally uses `-race`. Keep cgo enabled for that task. Linux needs an available C compiler; Windows amd64 needs a Go-compatible MinGW-w64 compiler with the required runtime libraries. A plain Git installation does not provide that compiler. Consult the [Go race detector requirements](https://go.dev/doc/articles/race_detector#Requirements) for the toolchain you use.

Check `go env GOOS GOARCH CGO_ENABLED CC` before diagnosing a race-build failure. On Windows, `gcc --print-file-name libsynchronization.a` should print an actual library path for a compatible compiler. Do not change machine configuration or install a compiler without the environment owner's consent.

Go does not provide race detection for every target, including Windows arm64. On an unsupported race target, run `go run ./tools/dev test`, `go vet ./...` and `go run ./tools/dev build` natively, record race detection as unavailable, and retain a race-enabled run on a supported target. Do not change `validate` to silently drop `-race` or describe the reduced check as the full validation gate.

The .NET lane acceptance is separate because it performs a NuGet restore and needs a .NET SDK plus feed access. Run it only when that restore is approved:

```powershell
$env:WORKCOPY_DOTNET_LANE_ACCEPTANCE = '1'
go test ./tests/acceptance -run '^TestSelectedDotNetProjectBuildsAndTestsInsideLane$' -count=1
```

The test confines its Git fixture, NuGet package cache and build outputs to temporary directories. The normal Go validation gate does not restore .NET dependencies.

The offline Node fixture also requires explicit script approval. It uses installed Node/npm, tiny local workspace scripts, no dependencies or installs, and private npm configuration/cache:

```powershell
$env:WORKCOPY_NODE_LANE_ACCEPTANCE = '1'
go test ./tests/acceptance -run '^TestSelectedNodeWorkspaceBuildsAndTestsInsideLane$' -count=1
```

It runs `npm --offline --ignore-scripts run build` and `run test`. The named scripts execute; pre/post lifecycle hooks are disabled. This proves the fixture's lane-local output and dependency selection, not arbitrary Node repositories.

## Local acceptance environment controls

The canonical `go run ./tools/dev test` and `go run ./tools/dev validate` tasks detect when the process temporary directory is inside a Git checkout and relocate child test processes to a local directory outside Git checkouts. If the host cannot provide a safe location, set `WORKCOPY_TEST_TEMP` to one explicitly. Do not move Workcopy's production workspace root inside a checkout to work around this boundary.

For the opt-in .NET lane acceptance, use a unique lane/cache directory and suppress first-run side effects in the child process. The fixture already applies these controls; real-project handoffs should do the same:

```powershell
$env:WORKCOPY_DOTNET_LANE_ACCEPTANCE = '1'
$env:DOTNET_CLI_TELEMETRY_OPTOUT = '1'
$env:DOTNET_SKIP_FIRST_TIME_EXPERIENCE = '1'
$env:DOTNET_GENERATE_ASPNET_CERTIFICATE = 'false'
$env:NUGET_PACKAGES = Join-Path $env:TEMP 'workcopy-nuget-agent'
go test ./tests/acceptance -run '^TestSelectedDotNetProjectBuildsAndTestsInsideLane$' -count=1
```

These variables prevent telemetry, certificate trust changes and persistent SDK first-run state. They do not grant access to a lane or replace host filesystem permissions.

