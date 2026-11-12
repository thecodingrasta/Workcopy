# Install and verify

Workcopy is one executable per platform. Needs Git 2.43 or newer on `PATH` (or named by `WORKCOPY_GIT`). Nothing else is required to run it; Go is needed only to build from source.

## Download

Take the files from [Releases](https://github.com/thecodingrasta/workcopy/releases) for the version you want:

| Platform | File |
| --- | --- |
| Windows x64 / arm64 | `workcopy_<version>_windows_amd64.exe`, `workcopy_<version>_windows_arm64.exe` |
| Linux x64 / arm64 | `workcopy_<version>_linux_amd64`, `workcopy_<version>_linux_arm64` |
| macOS Intel / Apple silicon | `workcopy_<version>_darwin_amd64`, `workcopy_<version>_darwin_arm64` |

Also download `SHA256SUMS` and the matching `workcopy_<version>_source.tar.gz` from the same release. The checksums file lists every file of that release, so one `SHA256SUMS` covers the binaries and the source. Never mix a checksums file with binaries from another release.

## Verify

Windows (PowerShell):

```powershell
$name = 'workcopy_v0.1.5_windows_amd64.exe'
$want = (Get-Content .\SHA256SUMS | Where-Object { $_.EndsWith("  $name") }).Split(' ')[0]
$got  = (Get-FileHash .\$name -Algorithm SHA256).Hash.ToLowerInvariant()
if ($got -ne $want) { throw "Checksum mismatch for $name" }
```

Linux:

```sh
grep '  workcopy_v0.1.5_linux_amd64$' SHA256SUMS | sha256sum -c -
```

macOS:

```sh
grep '  workcopy_v0.1.5_darwin_arm64$' SHA256SUMS | shasum -a 256 -c -
```

Replace the version and architecture with yours. A line that does not print `OK` (or a thrown mismatch) means the file is not the one that was released; do not run it.

## Put it on your PATH

Rename or link the file to `workcopy` (`workcopy.exe` on Windows) in a directory on your `PATH`. On Linux and macOS make it executable with `chmod +x`. Then check:

```sh
workcopy version     # the version you downloaded, plus platform and Go runtime
workcopy doctor      # Git program, line endings, filesystem warnings
```

On Windows with WSL, install the Windows build for repositories on a Windows drive and the Linux build for repositories in WSL; either accepts `C:\...` and `/mnt/c/...` paths. See [Windows, WSL and `/mnt/c`](wsl.md).

## Upgrade

Replace the executable. Lanes and their registrations are plain files and are read by the newer version; `workcopy doctor` reports anything it cannot use. Do not run two different versions against one lane root at the same moment.

## Build from source

```sh
go run ./tools/dev setup
go run ./tools/dev build
```

The executable lands in `dist/`. Packaging for release is described in [releasing](releasing.md).
