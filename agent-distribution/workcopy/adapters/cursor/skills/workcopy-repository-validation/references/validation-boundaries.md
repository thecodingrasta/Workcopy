# Validation boundaries

Workcopy's dependency discovery is advisory. It follows declared project relationships and reports known build-input gaps; it does not resolve every import, conditional SDK target, generated source input, external package, or custom build target.

- **Go:** inspect nested modules, `go.work`, replacements, vendoring, toolchain directives, and available cached modules. Use the repository's documented/tested command. Offline mode is appropriate only when all dependencies are already present.
- **.NET:** inspect the selected `.sln`/`.csproj`, project references, `global.json`, `Directory.Build.*`, central package files, and output paths. `workcopy_solution` can create an `.slnf` for `.sln` files; it does not build it. Restore only with approved feeds/cache and use lane-local output.
- **Node:** inspect lockfiles, workspaces, scripts, package manager identity, lifecycle hooks, and generated outputs. Installing dependencies or running scripts requires task authorization. Keep `node_modules` and outputs lane-local.
- **Other toolchains:** follow the repository's own validation docs and CI configuration. Do not invent test commands or claim tests ran unless the host process returned evidence.

If a command, SDK, package, service, or credential is unavailable, report the precise blocker and continue independent safe inspection. Never label a cross-compilation as native execution or a Workcopy plan as test success.
