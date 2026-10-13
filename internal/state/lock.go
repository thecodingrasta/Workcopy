package state

import (
	"os"
	"workcopy/internal/fsops"
	"workcopy/internal/model"
)

type Lock struct{ file *os.File }

func Acquire(root *fsops.Root, name string) (*Lock, error) {
	file, err := root.OpenLock(name)
	if err != nil {
		return nil, err
	}
	if err = lockFile(file); err != nil {
		file.Close()
		return nil, model.Fail(3, "workspace-busy", "Another Workcopy process holds the writer lock")
	}
	return &Lock{file}, nil
}
func (l *Lock) Close() error {
	if l.file == nil {
		return nil
	}
	err := unlockFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
