package workspace

import "testing"

// A mount table covering the layouts WSL users really have: the default
// /mnt/c, an extra mount of the same drive, [automount] root=/ (drives at /c),
// a WSL 1 drvfs mount whose point contains a space, a Linux filesystem mounted
// beneath a Windows drive, and 9p mounts that are not Windows drives.
const sampleMountInfo = `20 1 8:80 / / rw,relatime - ext4 /dev/sdf rw,discard
31 20 0:30 / /mnt/c rw,noatime - 9p C:\134 rw,dirsync,aname=drvfs;path=C:\;uid=0;gid=0;symlinkroot=/mnt/,mmap,access=client,msize=65536,trans=fd
32 20 0:31 / /mnt/cnm rw - 9p C:\134 rw,aname=drvfs;path=C:\;uid=0
33 20 0:32 / /c rw - 9p C:\134 rw,aname=drvfs;path=C:\
34 20 0:33 / /d\040data rw - drvfs D:\134 rw
35 20 0:34 / /mnt/wsl rw - tmpfs tmpfs rw
36 31 8:96 / /mnt/c/Users/me/linux-ext4 rw - ext4 /dev/sdg rw
37 20 0:36 / /srv/share rw - 9p share rw,trans=virtio
38 20 0:37 / /mnt/e rw - 9p E:\134 rw,uid=0
garbage line without a separator
`

func TestWindowsDriveDetectionFollowsTheMountTable(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/mnt/c/Users/me/repo", true},
		{"/mnt/c", true},
		{"/mnt/cnm/Users/me", true},             // an extra mount of C:\ that the /mnt/<letter> pattern cannot see
		{"/c/work/repo", true},                  // [automount] root=/
		{"/d data/project", true},               // drvfs on WSL 1, escaped space in the mount point
		{"/mnt/e/anything", true},               // recognised by its drive-letter source alone
		{"/mnt/c/Users/me/linux-ext4/x", false}, // the innermost mount decides
		{"/mnt/wsl/shared", false},
		{"/srv/share/data", false}, // 9p, but not a Windows drive
		{"/home/me/repo", false},
		{"/mnt/cc/repo", false},
		{"/mnt/cnmx/repo", false}, // a prefix of the mount point is not inside it
		{"/", false},
	}
	for _, c := range cases {
		if got := mountedFromWindows(sampleMountInfo, c.path); got != c.want {
			t.Errorf("%s: got %v, want %v", c.path, got, c.want)
		}
	}
	if mountedFromWindows("", "/mnt/c/x") {
		t.Error("an unreadable mount table must not claim a Windows drive")
	}
	// A later mount of the same point shadows an earlier one, as in the kernel.
	shadowed := "1 0 0:1 / /mnt/c rw - 9p C:\\134 rw,aname=drvfs\n2 0 8:1 / /mnt/c rw - ext4 /dev/sdz rw\n"
	if mountedFromWindows(shadowed, "/mnt/c/x") {
		t.Error("a Linux filesystem mounted over a drive must win")
	}
}

// The warning must follow the mount table end to end, not only the path pattern.
func TestDriveWarningsUseTheMountTableUnderWSL(t *testing.T) {
	was := readMountInfo
	t.Cleanup(func() { readMountInfo = was })
	readMountInfo = func() string { return sampleMountInfo }
	for path, want := range map[string]int{"/c/work/lanes": 1, "/mnt/cnm/lanes": 1, "/home/me/lanes": 0, "/mnt/c/Users/me/linux-ext4/lanes": 0} {
		got := driveWarnings("linux", true, []storageLocation{{"lane storage", path}})
		if len(got) != want {
			t.Errorf("%s: %d warnings, want %d", path, len(got), want)
		}
	}
	if got := driveWarnings("linux", false, []storageLocation{{"lane storage", "/c/work/lanes"}}); len(got) != 0 {
		t.Errorf("outside WSL the mount table must not matter: %v", got)
	}
	if got := driveWarnings("windows", false, []storageLocation{{"lane storage", "/c/work/lanes"}}); len(got) != 0 {
		t.Errorf("the Windows build must never warn: %v", got)
	}
}
