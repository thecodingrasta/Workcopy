//go:build linux || darwin

package acceptance

import "golang.org/x/sys/unix"

func allocatedFileBytes(filename string) (uint64, error) {
	var stat unix.Stat_t
	if err := unix.Lstat(filename, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Blocks) * 512, nil
}
