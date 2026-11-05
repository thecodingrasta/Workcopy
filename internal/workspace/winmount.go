package workspace

import (
	"os"
	"regexp"
	"strings"
)

// A Windows drive that WSL exposes is a 9p mount with aname=drvfs (WSL 2) or a
// drvfs mount (WSL 1). Its source is the drive, such as C:\. The mount point is
// usually /mnt/c but the owner can change it ([automount] root in wsl.conf puts
// drives at /c) and can add mounts of their own, so the mount table is the
// authority and the /mnt/<letter> pattern is only a fast path.
var driveLetterSource = regexp.MustCompile(`^[A-Za-z]:`)

// readMountInfo returns the kernel mount table. It is a variable so tests can
// supply one; it yields "" where the file does not exist (every non-Linux host).
var readMountInfo = func() string {
	body, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	return string(body)
}

// mountInfoEscapes undoes the octal escapes the kernel uses in mountinfo for
// characters that would break its space-separated format.
var mountInfoEscapes = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)

func within(mountPoint, path string) bool {
	return mountPoint == "/" || path == mountPoint || strings.HasPrefix(path, mountPoint+"/")
}

// mountedFromWindows reports whether the mount that holds path is a Windows
// drive. The innermost mount wins, so a Linux filesystem mounted beneath a
// Windows drive is not mistaken for it, and a later mount of the same point
// shadows an earlier one, as in the kernel.
func mountedFromWindows(mountInfo, path string) bool {
	windows, _ := innermostMount(mountInfo, path)
	return windows
}

// innermostMount returns whether the mount holding path is a Windows drive and
// the length of that mount point (-1 when the table lists no mount for it).
func innermostMount(mountInfo, path string) (windows bool, pointLength int) {
	bestLength := -1
	for _, line := range strings.Split(mountInfo, "\n") {
		head, tail, found := strings.Cut(line, " - ")
		if !found {
			continue
		}
		before, after := strings.Fields(head), strings.Fields(tail)
		if len(before) < 5 || len(after) < 3 {
			continue
		}
		point := mountInfoEscapes.Replace(before[4])
		if !within(point, path) || len(point) < bestLength {
			continue
		}
		bestLength = len(point)
		filesystem, source, options := after[0], mountInfoEscapes.Replace(after[1]), after[2]
		windows = filesystem == "drvfs" || driveLetterSource.MatchString(source) || (filesystem == "9p" && strings.Contains(options, "aname=drvfs"))
	}
	return windows, bestLength
}
