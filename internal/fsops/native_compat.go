//go:build !go1.25

package fsops

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// This backend requires cooperative writers. Newer builds use os.Root. Neither
// backend makes an agent with unrestricted host access into a sandboxed process.
const BackendDescription = "cooperative compatibility backend; Go 1.25+ is recommended"

type nativeRoot struct {
	directory string
	identity  os.FileInfo
}

func openNative(directory string) (*nativeRoot, error) {
	info, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	return &nativeRoot{directory, info}, nil
}
func (r *nativeRoot) verify() error {
	info, err := os.Lstat(r.directory)
	if err != nil {
		return err
	}
	if !os.SameFile(r.identity, info) || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("root identity changed")
	}
	return nil
}
func (r *nativeRoot) close() error { return nil }
func (r *nativeRoot) path(name string) string {
	return filepath.Join(r.directory, filepath.FromSlash(name))
}
func (r *nativeRoot) lstat(name string) (os.FileInfo, error) { return os.Lstat(r.path(name)) }
func (r *nativeRoot) openRead(name string) (*os.File, error) { return os.Open(r.path(name)) }
func (r *nativeRoot) create(name string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(r.path(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
}
func (r *nativeRoot) mkdir(name string, mode os.FileMode) error { return os.Mkdir(r.path(name), mode) }
func (r *nativeRoot) remove(name string) error                  { return os.Remove(r.path(name)) }
func (r *nativeRoot) rename(old, new string) error              { return os.Rename(r.path(old), r.path(new)) }
func (r *nativeRoot) readlink(name string) (string, error)      { return os.Readlink(r.path(name)) }
func (r *nativeRoot) symlink(target, name string) error         { return os.Symlink(target, r.path(name)) }
func (r *nativeRoot) filesystem() fs.FS                         { return os.DirFS(r.directory) }
