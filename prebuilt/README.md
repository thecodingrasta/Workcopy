# Bootstrap downloads

This directory contains the Windows amd64 **v0.1.0-rc.1 bootstrap candidate** and its matching source archive. It is a pre-release build, not a stable release or a tagged-build attestation. The source archive includes the pinned module source, build scripts and compiler information used to produce the executable.

Download the executable archive and `SHA256SUMS`. Matching source is available for rebuilding:

- [Windows amd64 executable and documentation](https://github.com/thecodingrasta/workcopy/raw/refs/heads/main/prebuilt/workcopy_v0.1.0-rc.1_windows_amd64.zip)
- [Matching source with pinned dependencies](https://github.com/thecodingrasta/workcopy/raw/refs/heads/main/prebuilt/workcopy_v0.1.0-rc.1_source.tar.gz)
- [SHA256SUMS](https://github.com/thecodingrasta/workcopy/raw/refs/heads/main/prebuilt/SHA256SUMS)

Verify each archive against its line in `SHA256SUMS`:

```powershell
Get-FileHash .\workcopy_v0.1.0-rc.1_windows_amd64.zip -Algorithm SHA256
Get-FileHash .\workcopy_v0.1.0-rc.1_source.tar.gz -Algorithm SHA256
Expand-Archive .\workcopy_v0.1.0-rc.1_windows_amd64.zip -DestinationPath .\workcopy
.\workcopy\workcopy.exe version
.\workcopy\workcopy.exe doctor
```

Add the extracted directory to your own `PATH` if desired. No installer, Go runtime or separate MCP package is required. Source rebuild instructions are in the archive's `BUILDINFO.txt`; use a supported patched Go toolchain.

Linux, macOS and other architectures are built by GitHub Actions. Use successful default-branch CI artifacts only; they are temporary and require GitHub sign-in. Maintainers rebuild the reviewed tag and publish durable downloads in [Releases](https://github.com/thecodingrasta/workcopy/releases), with matching source and checksums. Cross-compilation is not native validation.
