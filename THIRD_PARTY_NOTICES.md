# Third-party notices

Workcopy's source is licensed under [GNU GPL v3 or later](LICENSE), as stated in [COPYRIGHT](COPYRIGHT). Third-party components retain the licences below.

The Go module pins the official Model Context Protocol SDK v1.7.0 and its runtime dependencies. The Git repository does not commit a vendor tree; release source archives include pinned module source. Exact upstream licence texts are preserved in [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md), which is included in release archives. The SDK's licence records its Apache-2.0/MIT licensing transition; do not describe all SDK code as MIT-only.

| Module | Version | Upstream licence |
| --- | --- | --- |
| github.com/modelcontextprotocol/go-sdk | v1.7.0 | Apache-2.0 / retained MIT contributions |
| github.com/google/jsonschema-go | v0.4.3 | BSD-3-Clause |
| github.com/segmentio/encoding | v0.5.4 | MIT |
| github.com/segmentio/asm | v1.1.3 | MIT |
| github.com/yosida95/uritemplate/v3 | v3.0.2 | MIT |
| golang.org/x/oauth2 | v0.35.0 | BSD-3-Clause |
| golang.org/x/sync | v0.20.0 | BSD-3-Clause |
| golang.org/x/sys | v0.44.0 | BSD-3-Clause |
| golang.org/x/time | v0.15.0 | BSD-3-Clause |

`go.mod` and `go.sum` are the authoritative dependency/version records. Compiled binaries also include the Go runtime and standard library; the release task copies the build toolchain's complete licence into every archive as `GO-LICENSE`.

Git is invoked as a separately installed executable and is not bundled. Git's own licensing remains applicable to that installation.

GitHub Actions used by the repository are external workflow dependencies, not code incorporated into the Workcopy executable. Their immutable revisions are recorded in the workflow files. No third-party fonts, images or branding assets are distributed.
