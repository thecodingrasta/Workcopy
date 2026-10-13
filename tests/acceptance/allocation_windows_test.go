//go:build windows

package acceptance

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

func allocatedFileBytes(filename string) (uint64, error) {
	name, err := windows.UTF16PtrFromString(filename)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)
	var info struct {
		AllocationSize int64
		EndOfFile      int64
		NumberOfLinks  uint32
		DeletePending  byte
		Directory      byte
		Padding        [2]byte
	}
	if err = windows.GetFileInformationByHandleEx(handle, windows.FileStandardInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return 0, err
	}
	return uint64(info.AllocationSize), nil
}
