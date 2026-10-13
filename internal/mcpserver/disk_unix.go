//go:build linux || darwin

package mcpserver

import "golang.org/x/sys/unix"

func freeSpace(directory string) (uint64, error) {
	ancestor, err := existingAncestor(directory)
	if err != nil {
		return 0, err
	}
	var stat unix.Statfs_t
	if err = unix.Statfs(ancestor, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
