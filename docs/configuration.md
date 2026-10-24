# Configuration

Workcopy reads `.workcopy.json` from the selected committed snapshot. An uncommitted edit in the main checkout is not configuration for a new lane. `--config FILE` explicitly reads a local file instead.

Schema version 1 uses strict JSON. Unknown properties, duplicate keys, invalid UTF-8, multiple JSON documents and a missing schema version are rejected. There is no executable configuration, environment substitution or `.env` file.

```json
{
  "schemaVersion": 1,
  "defaultProfile": "daily",
  "discovery": { "adapters": ["generic"] },
  "projects": [
    { "id": "api", "path": "src/Api", "dependencies": ["core"] },
    { "id": "core", "path": "src/Core" },
    { "id": "reports", "path": "src/Reports" }
  ],
  "profiles": {
    "daily": {
      "projects": ["api", "core"],
      "include": ["shared/**"],
      "exclude": ["**/bin/**", "**/obj/**"],
      "dependencyClosure": "strict"
    }
  }
}
```

Project paths are repository-relative directories. A path of `.` selects the repository root. Explicit IDs remain stable even when directory names change in a later configuration. No framework names or dependency directories are excluded automatically from tracked source.

Files at the repository root are included by default, then explicit exclusions apply. An ignored local directory is not imported just because it is under a selected project. Later source additions are discovered with the lane's ignore rules, using an isolated Git index rather than the main checkout's staging area.

## Selection rules

Without a profile or explicit project selection, all tracked source is selected. With a committed default profile, `create` uses that profile. `--all` bypasses the default profile; it cannot be combined with `--profile` or `--project`.

`--project` accepts a configured/discovered ID or a literal directory path. It may be repeated. A named profile and explicit projects are additive. `expand` adds projects or paths without replacing edited files. Repeating an expansion that adds no source returns exit code 4.

A directory matching one known project resolves to that project's identity and enforces its dependency closure, just like its ID. Ambiguous directories require explicit IDs. Other paths remain generic source selections; they cannot infer arbitrary build relationships.

Patterns use `/` on every platform. `*`, `?` and character classes match within a path segment; a `**` segment matches recursively. Exclusions take precedence. Absolute paths and traversal components are rejected.

`dependencyClosure: "strict"` requires dependencies to be explicitly selected. `"include"` adds known dependencies transitively. Unknown relationships are not guessed. A profile excluding an essential shared import may still fail to build.

A lane stores its resolved configuration and selection. Editing the repository configuration does not silently change an existing lane. Create a new lane to apply a new profile definition.

## Discovery

Without a configuration file, discovery enables `auto`. With an explicit configuration, only the listed adapters are enabled; an omitted adapter list means explicit projects only.

| Adapter | Reads | Does not evaluate |
| --- | --- | --- |
| `generic` | Explicit project paths and relationships | Language or build-system dependencies |
| `dotnet` | `.csproj`, `.fsproj`, `.vbproj` and literal `ProjectReference` paths | MSBuild imports, properties, SDK execution or full condition semantics |
| `node` | Tracked `package.json` files and matching local package names | Package-manager installation layout or every workspace/glob convention |
| `go` | Tracked `go.mod` directory boundaries | Import graphs or automatic `go.work` rewrites |
| `auto` | The three language adapters above | Any build or package restore |

Conditional .NET references are conservatively included where detected. Expressions that need MSBuild evaluation produce warnings. Explicit paths and dependency declarations are the fallback for custom layouts. Discovery is not a build-readiness verdict.

Literal references outside the repository produce warnings and are never followed/copied. They do not block discovery of independent projects. Projects requiring them still need an explicit isolated dependency arrangement or configured package mode before building.

The [JSON schema](../schema/workcopy.schema.json) describes configuration fields. The [examples](../examples/README.md) include a 25-project repository with a ten-project daily profile.

### Discovery warnings and case-sensitive repositories

A literal .NET project reference that does not match a discovered project is reported as a warning rather than aborting the entire catalog. Independent projects remain discoverable, but the affected project may need an explicit project/dependency selection before it can build. A reference that differs only by path case is resolved to the tracked project and reported with a warning; correct the source spelling for Linux/WSL and other case-sensitive filesystems because discovery cannot repair the build tool's own path resolution.

