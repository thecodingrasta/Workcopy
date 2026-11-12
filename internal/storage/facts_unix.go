//go:build linux || darwin

package storage

import (
	"fmt"
	"os"
	"syscall"
)

// Facts reads one file without following a symbolic link. Allocation is the
// number of 512-byte blocks the kernel reports, which already excludes holes in
// sparse files.
func Facts(path string) (FileFacts, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return FileFacts{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return FileFacts{}, fmt.Errorf("no inode information for %s", path)
	}
	return FileFacts{
		ID:        FileID{Volume: uint64(stat.Dev), Low: uint64(stat.Ino)},
		Links:     uint32(stat.Nlink),
		Logical:   stat.Size,
		Allocated: stat.Blocks * 512,
	}, nil
}
