//go:build go1.25

package fsops

import (
	"io/fs"
	"os"
)

const BackendDescription = "Go os.Root backend"

type nativeRoot struct{ root *os.Root }

func openNative(directory string) (*nativeRoot, error) {
	r, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &nativeRoot{r}, nil
}

// os.Root operations retain their root handle and enforce traversal containment.
func (r *nativeRoot) verify() error                          { return nil }
func (r *nativeRoot) close() error                           { return r.root.Close() }
func (r *nativeRoot) lstat(name string) (os.FileInfo, error) { return r.root.Lstat(name) }
func (r *nativeRoot) openRead(name string) (*os.File, error) { return r.root.Open(name) }
func (r *nativeRoot) create(name string, mode os.FileMode) (*os.File, error) {
	return r.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
}
func (r *nativeRoot) mkdir(name string, mode os.FileMode) error { return r.root.Mkdir(name, mode) }
func (r *nativeRoot) remove(name string) error                  { return r.root.Remove(name) }
func (r *nativeRoot) rename(old, new string) error              { return r.root.Rename(old, new) }
func (r *nativeRoot) readlink(name string) (string, error)      { return r.root.Readlink(name) }
func (r *nativeRoot) symlink(target, name string) error         { return r.root.Symlink(target, name) }
func (r *nativeRoot) filesystem() fs.FS                         { return r.root.FS() }
