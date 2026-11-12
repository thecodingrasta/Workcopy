//go:build windows

package storage

import (
	"encoding/binary"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fileStandardInfoClass = 1
	fileIDInfoClass       = 18
)

// fileStandardInfo mirrors FILE_STANDARD_INFO (24 bytes).
type fileStandardInfo struct {
	AllocationSize int64
	EndOfFile      int64
	NumberOfLinks  uint32
	DeletePending  uint8
	Directory      uint8
	_              [2]uint8
}

// fileIDInfo mirrors FILE_ID_INFO (24 bytes): a 128-bit identifier that stays
// unique on ReFS, where the older 64-bit index can collide.
type fileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

// extendedPath returns an absolute path that Windows will not shorten or limit
// to MAX_PATH.
func extendedPath(path string) string {
	if strings.HasPrefix(path, `\\?\`) {
		return path
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	if strings.HasPrefix(absolute, `\\`) {
		return `\\?\UNC\` + absolute[2:]
	}
	return `\\?\` + absolute
}

// Facts reads one file without following a reparse point. It asks for no access
// rights, so a file another process holds open exclusively can still be read.
func Facts(path string) (FileFacts, error) {
	name, err := windows.UTF16PtrFromString(extendedPath(path))
	if err != nil {
		return FileFacts{}, err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return FileFacts{}, err
	}
	defer windows.CloseHandle(handle)
	var basic windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &basic); err != nil {
		return FileFacts{}, err
	}
	var standard fileStandardInfo
	if err = windows.GetFileInformationByHandleEx(handle, fileStandardInfoClass, (*byte)(unsafe.Pointer(&standard)), uint32(unsafe.Sizeof(standard))); err != nil {
		return FileFacts{}, err
	}
	facts := FileFacts{Links: basic.NumberOfLinks, Logical: standard.EndOfFile, Allocated: standard.AllocationSize}
	var identifier fileIDInfo
	if err = windows.GetFileInformationByHandleEx(handle, fileIDInfoClass, (*byte)(unsafe.Pointer(&identifier)), uint32(unsafe.Sizeof(identifier))); err == nil {
		facts.ID = FileID{Volume: identifier.VolumeSerialNumber, Low: binary.LittleEndian.Uint64(identifier.FileID[:8]), High: binary.LittleEndian.Uint64(identifier.FileID[8:])}
		return facts, nil
	}
	facts.ID = FileID{Volume: uint64(basic.VolumeSerialNumber), Low: uint64(basic.FileIndexHigh)<<32 | uint64(basic.FileIndexLow)}
	return facts, nil
}
