# Workcopy installation checks

Workcopy releases are native executables. Supported release names are `workcopy_v<VERSION>_<GOOS>_<GOARCH>` (with `.exe` on Windows) and `SHA256SUMS`, published at `https://github.com/thecodingrasta/Workcopy/releases`.

## Existing executable

First check `workcopy version` and `workcopy doctor`. If the version satisfies the package's stated minimum and the reported platform matches the host process, keep the existing installation. `doctor` is read-only.

For Workcopy's paired Agent Plugin package, use the exact Workcopy release version recorded in the package's root `plugin.json`. If the installed executable reports another version, explain the mismatch and stop before starting MCP; do not replace the executable without the user's request.

## Select and verify an official binary

Choose the release asset from the host process OS and architecture, not the user's browser or repository path. Download the binary and that release's `SHA256SUMS` from the same HTTPS GitHub release page. Find the exact filename row, calculate SHA-256 locally (`Get-FileHash -Algorithm SHA256` on Windows, `sha256sum` on Linux, or `shasum -a 256` on macOS), and compare the full digest before moving or running the file. A checksum served alongside the binary detects transfer corruption; it is not an independent publisher signature.

Install in a user-owned directory only after verification. Do not replace an existing executable without explicit consent. Do not require administrator privileges. Add the directory to the user's PATH only as a separate, reviewable choice; restart the harness so it inherits the updated PATH. Then run `workcopy version` and `workcopy doctor`.

If the release or checksum asset is unavailable, stop and report that. Do not substitute a CI artifact, third-party mirror, source bootstrap binary, or unverified download without the user's choice.

## Platform notes

- **Windows:** use the Windows executable and Windows absolute paths. `workcopy.exe` must be visible to the host process. A WSL-launched harness may have a different PATH from Windows desktop apps.
- **WSL:** use either the Linux executable with Linux paths and a WSL-native workspace root, or the Windows executable with Windows paths. Do not pass `/mnt/c/...` to a Windows binary or `C:\...` to a Linux binary. Linux lanes on `/mnt/c` are supported but slow; ext4 storage is preferred.
- **Linux/macOS:** install the matching native binary under a user-owned directory. Confirm `git`, `workcopy`, and the workspace root are visible in the same process environment as the harness.
- **Cloud execution:** installation is inside that cloud execution environment. It does not give the cloud harness access to a local checkout on another device.
