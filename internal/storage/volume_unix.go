//go:build linux || darwin

package storage

import (
	"fmt"
	"syscall"
)

// Describe reads the filesystem type and free space. fileSystemInfo is
// implemented per operating system because the kernels report the type
// differently.
func Describe(path string) (Volume, error) {
	existing, err := existingAncestor(path)
	if err != nil {
		return Volume{}, err
	}
	var stat syscall.Stat_t
	if err = syscall.Stat(existing, &stat); err != nil {
		return Volume{}, err
	}
	volume := Volume{Path: existing, ID: fmt.Sprintf("dev:%d", uint64(stat.Dev)), HardLinks: Unknown, BlockClone: Unknown}
	volume.FileSystem, volume.HardLinks, volume.BlockClone, volume.CloneBasis, volume.FreeBytes, volume.TotalBytes, err = fileSystemInfo(existing)
	if err != nil {
		return Volume{}, err
	}
	return volume, nil
}
