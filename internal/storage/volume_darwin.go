//go:build darwin

package storage

import "golang.org/x/sys/unix"

func fileSystemInfo(path string) (name string, links, clone Support, basis string, free, total uint64, err error) {
	var stat unix.Statfs_t
	if err = unix.Statfs(path, &stat); err != nil {
		return
	}
	free, total = uint64(stat.Bavail)*uint64(stat.Bsize), uint64(stat.Blocks)*uint64(stat.Bsize)
	name = unix.ByteSliceToString(stat.Fstypename[:])
	switch name {
	case "apfs":
		return name, Yes, Yes, "APFS clones files with clonefile(2) within one volume", free, total, nil
	case "hfs":
		return name, Yes, No, "HFS+ has no clonefile support", free, total, nil
	case "msdos", "exfat":
		return name, No, No, "FAT has neither hard links nor cloning", free, total, nil
	}
	return name, Unknown, Unknown, "filesystem type is not in Workcopy's table", free, total, nil
}
