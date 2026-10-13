package fsops

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"workcopy/internal/model"
)

const DirectoryLinkStrategy = "junction"

func (r *Root) ReplaceDirectoryLink(name, target string) error {
	if err := r.guard(name, true); err != nil {
		return err
	}
	target = strings.TrimPrefix(target, `\\?\`)
	target = filepath.Clean(filepath.FromSlash(target))
	buffer, err := junctionBuffer(target)
	if err != nil {
		return err
	}
	if err = r.MkdirAll(path.Dir(name), 0700); err != nil {
		return err
	}
	id, err := model.ID()
	if err != nil {
		return err
	}
	temporary := path.Join(path.Dir(name), ".workcopy-tmp-"+id)
	if err = r.backend.mkdir(temporary, 0700); err != nil {
		return err
	}
	defer r.backend.remove(temporary)
	full := filepath.Join(r.Path, filepath.FromSlash(temporary))
	pointer, err := syscall.UTF16PtrFromString(full)
	if err != nil {
		return err
	}
	handle, err := syscall.CreateFile(pointer, syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OPEN_REPARSE_POINT|syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return &os.PathError{Op: "create-junction", Path: name, Err: err}
	}
	var returned uint32
	controlErr := syscall.DeviceIoControl(handle, 0x000900a4, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil)
	closeErr := syscall.CloseHandle(handle)
	if controlErr != nil {
		return fmt.Errorf("create NTFS junction: %w", controlErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if err = r.guard(name, true); err != nil {
		return err
	}
	return r.backend.rename(temporary, name)
}
