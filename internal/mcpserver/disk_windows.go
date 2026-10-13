//go:build windows

package mcpserver

import "golang.org/x/sys/windows"

func freeSpace(directory string) (uint64, error) {
	ancestor, err := existingAncestor(directory)
	if err != nil {
		return 0, err
	}
	name, err := windows.UTF16PtrFromString(ancestor)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	err = windows.GetDiskFreeSpaceEx(name, &available, &total, &free)
	return available, err
}
