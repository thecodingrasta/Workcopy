package state

import (
	"os"
	"syscall"
	"unsafe"
)

var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
var unlockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")

// Keep the coordination byte outside the lock file's normal contents. Windows
// rejects reads that overlap an exclusive byte-range lock, and the lane scan
// reads .workcopy/lock while its writer lock is held.
const lockOffset = 1

func lockFile(file *os.File) error {
	overlapped := syscall.Overlapped{Offset: lockOffset}
	result, _, err := lockFileEx.Call(file.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result == 0 {
		return err
	}
	return nil
}
func unlockFile(file *os.File) error {
	overlapped := syscall.Overlapped{Offset: lockOffset}
	result, _, err := unlockFileEx.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result == 0 {
		return err
	}
	return nil
}
