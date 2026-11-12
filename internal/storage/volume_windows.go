//go:build windows

package storage

import (
	"fmt"

	"golang.org/x/sys/windows"
)

const (
	fileSupportsHardLinks     = 0x00400000
	fileSupportsBlockRefcount = 0x08000000
)

// Describe reads the volume flags Windows reports. Block cloning is taken from
// FILE_SUPPORTS_BLOCK_REFCOUNTING, which Microsoft documents as meaning that
// FSCTL_DUPLICATE_EXTENTS_TO_FILE is supported, not from the filesystem name.
func Describe(path string) (Volume, error) {
	existing, err := existingAncestor(path)
	if err != nil {
		return Volume{}, err
	}
	name, err := windows.UTF16PtrFromString(existing)
	if err != nil {
		return Volume{}, err
	}
	rootBuffer := make([]uint16, 1024)
	if err = windows.GetVolumePathName(name, &rootBuffer[0], uint32(len(rootBuffer))); err != nil {
		return Volume{}, err
	}
	volume := Volume{Path: existing, HardLinks: Unknown, BlockClone: Unknown}
	root := windows.UTF16ToString(rootBuffer)
	rootPointer, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return Volume{}, err
	}
	guid := make([]uint16, 64)
	if windows.GetVolumeNameForVolumeMountPoint(rootPointer, &guid[0], uint32(len(guid))) == nil {
		volume.ID = windows.UTF16ToString(guid)
	}
	var serial, maximumComponent, flags uint32
	fileSystem := make([]uint16, 64)
	if err = windows.GetVolumeInformation(rootPointer, nil, 0, &serial, &maximumComponent, &flags, &fileSystem[0], uint32(len(fileSystem))); err != nil {
		return Volume{}, err
	}
	if volume.ID == "" {
		volume.ID = fmt.Sprintf("serial:%08X", serial)
	}
	volume.FileSystem = windows.UTF16ToString(fileSystem)
	volume.HardLinks = No
	if flags&fileSupportsHardLinks != 0 {
		volume.HardLinks = Yes
	}
	volume.BlockClone = No
	volume.CloneBasis = "volume flag FILE_SUPPORTS_BLOCK_REFCOUNTING is not set"
	if flags&fileSupportsBlockRefcount != 0 {
		volume.BlockClone = Yes
		volume.CloneBasis = "volume flag FILE_SUPPORTS_BLOCK_REFCOUNTING is set"
	}
	var available, total, free uint64
	if windows.GetDiskFreeSpaceEx(rootPointer, &available, &total, &free) == nil {
		volume.FreeBytes, volume.TotalBytes = available, total
	}
	return volume, nil
}
