# Configuration examples

`monorepo.workcopy.json` declares 25 generic project paths under `src/project-01` through `src/project-25`. Its `daily` profile selects the first ten. It is data for that example layout, not an assumption about your repository.

Copy the example to your repository as `.workcopy.json`, replace the project declarations with real paths and commit it. For an uncommitted local trial, pass its path explicitly with `--config`. `create --dry-run` validates selected paths before writing a lane.

`minimal.workcopy.json` selects the whole repository through explicit generic discovery. `dotnet.workcopy.json` enables .NET discovery; it does not run MSBuild or guarantee buildability.

See [configuration](../docs/configuration.md) for selection and dependency rules. The integration tests synthesize their own repositories rather than depending on a developer's private project layout.
