package fsops

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"

	"workcopy/internal/model"
)

var windowsAbsolute = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// CheckNativePath rejects a path written for the other operating system with a
// message that says what to pass instead. In hybrid setups (a Windows
// workcopy.exe driven from WSL, or the reverse) the raw failure is a Git error
// about a directory such as C:\mnt\c\Users that never existed.
func CheckNativePath(path string) error {
	return checkNativePath(path, runtime.GOOS)
}

func checkNativePath(path, goos string) error {
	if goos == "windows" {
		if strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") {
			return model.Fail(2, "posix-path-on-windows", fmt.Sprintf("%q is a POSIX path, but this Workcopy build runs on Windows. Pass a Windows path such as C:\\Users\\you\\repo (from WSL, `wslpath -w %s` converts it).", path, path))
		}
		return nil
	}
	if windowsAbsolute.MatchString(path) || strings.HasPrefix(path, `\\`) {
		return model.Fail(2, "windows-path-on-posix", fmt.Sprintf("%q is a Windows path, but this Workcopy build runs on %s. Pass a POSIX path (from WSL, `wslpath -u '%s'` converts it, for example /mnt/c/Users/you/repo).", path, goos, path))
	}
	return nil
}
