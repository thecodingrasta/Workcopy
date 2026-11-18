# Agent Distribution Toolkit

`go run ./tools/agentdist` is a small Go-native toolkit for validating and packaging a canonical Agent Plugin and thin harness adapters. It adds no end-user runtime dependency. Its `--config` flag accepts any repository-relative distribution definition, so another product can reuse the tool without changing its implementation.

## Commands

```sh
go run ./tools/agentdist validate
go run ./tools/agentdist sync-skills
go run ./tools/agentdist package --output dist
go run ./tools/agentdist validate --config agent-distribution/other-product/agentdist.json
```

`sync-skills` writes canonical skills and repository-level license notices into the `publishSource` adapter directories used directly by repository marketplace manifests. It creates missing generated files, refuses stale copies unless `--overwrite` is explicit, and never deletes extra files. Review/remove obsolete generated files yourself; validation reports them. Published sources also require a nonempty native README and byte-for-byte current license notices. `validate` checks these published copies against the canonical tree, Agent Plugins 1.0 manifests and MCP configuration, Agent Skills names/frontmatter/resources/progressive-disclosure limits, native adapter layouts and MCP configuration, package paths, release metadata and licenses. A product definition may point to its own Go MCP registration source, typed contract examples, and CLI help constant; when supplied, the toolkit parses these real sources and validates tool names and argument types. Products without those Go entry points can omit the optional contract fields.

`package` builds deterministic ZIP files with a stable timestamp, sorted paths, canonical skills, native target files, license notices and marketplace metadata. Adapter source trees may contain native binaries; declare their paths under `executableFiles` to preserve executable mode in the ZIP. It writes one `AGENT_PLUGIN_SHA256SUMS` file and refuses to overwrite any output. The Workcopy release workflow packages the exact reviewed tag after native validation; it never publishes a marketplace listing.

## Reuse for another product

For another product in this repository (or in a copy of the toolkit), provide a product-owned directory and pass its `agentdist.json` path through `--config`. It should contain:

- a portable plugin root with `plugin.json`, optional `mcp.json`, and canonical `skills/`;
- adapter directories containing only native manifest/configuration files and short setup README content;
- `agentdist.json` with a semantic default version, product license, root notice files, adapter IDs, skill destinations, archive names, optional repository publication sources, and optional MCP/CLI source and contract-example paths. Declare the product's MCP tool prefix and CLI command name with those source paths so validation does not assume Workcopy's identifiers;
- contract examples whose argument names and types match the actual MCP input structs;
- `marketplace-metadata.json` with truthful publisher, platform, privacy, and capability claims.

The toolkit accepts any Go module name and product license through the selected definition. Its default `--config` points to Workcopy, while another product can use `--config agent-distribution/other-product/agentdist.json`; an isolated fixture in the test suite proves validation with a different module, MIT license, `acme_` MCP prefix and `sample-cli` name. The toolkit deliberately does not download binaries, install packages, edit user configuration, create lanes, submit listings, or run harnesses. A product may extend its metadata and adapter templates without introducing a second runtime or a maintained copy of skill instructions; generated adapter copies are checked against the canonical skills source when packaged.
