package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"workcopy/internal/fsops"
	"workcopy/internal/git"
)

var windowsDrivePath = regexp.MustCompile(`^/mnt/[a-zA-Z](/|$)`)

// underWSL reports whether this process runs inside Windows Subsystem for
// Linux. It is a variable so tests can exercise the warnings on any host.
var underWSL = func() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	release, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(release)), "microsoft")
}

// onWindowsDrive reports whether a path is on a Windows drive as WSL mounts it,
// at /mnt/c or wherever the mount table says one is. Such mounts are 9p/drvfs:
// slow, and they hide metadata.
func onWindowsDrive(goos string, wsl bool, path string) bool {
	if goos != "linux" || !wsl {
		return false
	}
	// The mount table decides when it has an answer. The /mnt/<letter> pattern is
	// the fallback for a table that is unreadable or whose entry for the drive is
	// not recognised, and it never overrides a mount nested beneath the drive.
	if windows, length := innermostMount(readMountInfo(), path); windows {
		return true
	} else if match := windowsDrivePath.FindString(path); match != "" {
		return length <= len(strings.TrimSuffix(match, "/"))
	}
	return false
}

type storageLocation struct{ what, path string }

// laneStorageAdvice is the one change that fixes slow lane operations under
// WSL. Measured on this project (1,000 files, repository on /mnt/c): creating a
// lane took 6.0 s with lane storage on /mnt/c and 0.5 s with it on the WSL
// filesystem, and a review 9.4 s against 0.8 s. Mount options (msize,
// cache=loose) changed less than 10%, because every create, rename and close is
// a round trip to Windows that the host serialises.
const laneStorageAdvice = "Set WORKCOPY_ROOT in the Workcopy settings file (or pass --root) to a directory on the WSL filesystem, for example ~/.workcopy/lanes; the repository can stay where it is."

// driveWarnings explains what goes wrong when a repository or lane storage
// lives on a Windows drive but the Linux build is driving it.
func driveWarnings(goos string, wsl bool, locations []storageLocation) []string {
	warnings := []string{}
	for _, location := range locations {
		if !onWindowsDrive(goos, wsl, location.path) {
			continue
		}
		if location.what == "lane storage" {
			warnings = append(warnings, fmt.Sprintf("The lane storage %s is on a Windows drive seen from WSL (drvfs/9p). Every file operation there is a round trip to Windows, so creating, reviewing and syncing a lane is roughly ten times slower than on the WSL filesystem. %s", location.path, laneStorageAdvice))
			continue
		}
		warnings = append(warnings, fmt.Sprintf("The %s %s is on a Windows drive seen from WSL (drvfs/9p). Reading it works, but a review worktree written by Linux git is invalid for Windows git (it shows as prunable), and a symlink made by Share is unreadable from Windows. Use the Windows workcopy.exe when Windows tools must see the lanes, or keep the repository on the WSL filesystem.", location.what, location.path))
	}
	return warnings
}

// LaneStorageWarning is the warning shown when a lane is created on a Windows
// drive from WSL, or "" when it does not apply.
func LaneStorageWarning(workRoot string) string {
	if warnings := driveWarnings(runtime.GOOS, runtime.GOOS == "linux" && underWSL(), []storageLocation{{"lane storage", workRoot}}); len(warnings) > 0 {
		return warnings[0]
	}
	return ""
}

// Doctor describes the runtime, the Git program and, when a repository can be
// found, how lanes for it will behave. It reads configuration only; it creates
// no files and runs no repository code.
func Doctor(ctx context.Context, version, repository, root string) (Report, error) {
	report := Report{Command: "doctor"}
	gitPath, err := git.Executable()
	if err != nil {
		return report, err
	}
	output, err := exec.CommandContext(ctx, gitPath, "--version").Output()
	if err != nil {
		return report, err
	}
	lines := []string{
		fmt.Sprintf("Workcopy %s", version),
		fmt.Sprintf("Runtime: %s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH),
		fmt.Sprintf("Git: %s (%s)", strings.TrimSpace(string(output)), gitPath),
		"Filesystem: " + fsops.BackendDescription,
	}
	if override := os.Getenv(git.ExecutableEnvironment); override != "" {
		lines = append(lines, fmt.Sprintf("Git program chosen by %s=%s", git.ExecutableEnvironment, override))
	}
	report.Warnings = []string{"A Git-free lane prevents accidental Git operations; it does not sandbox an agent with access to the source repository.", "Writable shared dependency links are cooperative, not read-only isolation."}

	if repository == "" {
		if here, cwdErr := os.Getwd(); cwdErr == nil {
			repository = here
		}
	}
	if engine, newErr := New(ctx, repository, root); newErr == nil {
		lines = append(lines, "Repository: "+engine.Repository.Root, "Lane storage: "+engine.WorkRoot)
		if config, configErr := engine.Repository.CheckoutConfig(ctx); configErr == nil {
			ending := "LF"
			if engine.Repository.NativeCRLF() {
				ending = "CRLF"
			}
			lines = append(lines, fmt.Sprintf("Line endings: core.autocrlf=%s core.eol=%s (native is %s); lanes mirror what git checkout would write", config.AutoCRLF, config.EOL, ending), fmt.Sprintf("Execute bits: core.fileMode=%t", config.FileMode))
		}
		report.Warnings = append(report.Warnings, driveWarnings(runtime.GOOS, runtime.GOOS == "linux" && underWSL(), []storageLocation{{"repository", engine.Repository.Root}, {"lane storage", engine.WorkRoot}})...)
	} else if repository != "" {
		lines = append(lines, "No Git repository found at "+repository+"; repository-specific checks were skipped.")
	}
	lines = append(lines, "No installations, package restores, or repository changes were performed.")
	report.Message = strings.Join(lines, "\n")
	return report, nil
}
