// Package fsops centralizes portable path policy and independent file replacement.
package fsops

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"workcopy/internal/model"
)

type Root struct {
	Path    string
	backend *nativeRoot
}

func OpenRoot(directory string) (*Root, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("root must be a real directory: %s", absolute)
	}
	b, err := openNative(absolute)
	if err != nil {
		return nil, err
	}
	return &Root{Path: absolute, backend: b}, nil
}
func (r *Root) Close() error { return r.backend.close() }
func ValidateRelative(name string) error {
	if !utf8.ValidString(name) || name == "" || name == "." || path.IsAbs(name) || strings.Contains(name, "\\") || path.Clean(name) != name {
		return fmt.Errorf("invalid relative path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == "" || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return fmt.Errorf("unsafe path %q", name)
		}
		for _, ch := range part {
			if ch < 32 || ch == 127 || unicode.IsControl(ch) || strings.ContainsRune(`<>:"|?*`, ch) {
				return fmt.Errorf("nonportable path %q", name)
			}
		}
		base := strings.ToUpper(strings.Split(part, ".")[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$" || (len([]rune(base)) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && strings.ContainsRune("0123456789¹²³", []rune(base)[3])) {
			return fmt.Errorf("reserved path %q", name)
		}
	}
	return nil
}
func ValidateSourcePath(name string) error {
	if err := ValidateRelative(name); err != nil {
		return err
	}
	for _, part := range strings.Split(name, "/") {
		if strings.EqualFold(part, ".git") || strings.EqualFold(part, ".workcopy") || strings.EqualFold(part, "git~1") {
			return fmt.Errorf("reserved source path %q", name)
		}
	}
	return nil
}
func (r *Root) guard(name string, parents bool) error {
	if err := ValidateRelative(name); err != nil {
		return err
	}
	if err := r.backend.verify(); err != nil {
		return err
	}
	if !parents {
		return nil
	}
	components := strings.Split(name, "/")
	for n := 1; n < len(components); n++ {
		parent := strings.Join(components[:n], "/")
		info, err := r.backend.lstat(parent)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			// A file beneath an ordinary file is absent. Symlink parents remain a hard
			// confinement error rather than masquerading as an absent child.
			return &os.PathError{Op: "lookup", Path: name, Err: os.ErrNotExist}
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("parent is not a real directory: %s", parent)
		}
	}
	return nil
}
func (r *Root) Lstat(name string) (os.FileInfo, error) {
	if err := r.guard(name, true); err != nil {
		return nil, err
	}
	return r.backend.lstat(name)
}
func (r *Root) ReadFile(name string) ([]byte, error) {
	return r.ReadFileLimit(name, model.MaxFileBytes)
}
func (r *Root) ReadFileLimit(name string, limit int64) ([]byte, error) {
	info, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	return r.ReadRegular(name, info, limit)
}

// ReadRegular reads a file the caller has just examined with Lstat, so bulk
// readers do not pay for the parent-directory checks and the stat twice.
// info must come from r.Lstat(name); the open is still verified against it.
func (r *Root) ReadRegular(name string, info os.FileInfo, limit int64) ([]byte, error) {
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", name)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("file exceeds %d byte limit: %s", limit, name)
	}
	file, err := r.backend.openRead(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		return nil, fmt.Errorf("file changed while opening: %s", name)
	}
	if actual.Size() < 0 || actual.Size() > limit {
		return nil, fmt.Errorf("file grew beyond limit: %s", name)
	}
	// One spare byte detects growth without the repeated allocations of ReadAll.
	body := make([]byte, int(actual.Size())+1)
	n, readErr := io.ReadFull(file, body)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return nil, readErr
	}
	if int64(n) != actual.Size() {
		return nil, fmt.Errorf("file changed size while reading: %s", name)
	}
	return body[:n], nil
}
func (r *Root) Readlink(name string) (string, error) {
	if err := r.guard(name, true); err != nil {
		return "", err
	}
	return r.backend.readlink(name)
}
func (r *Root) MkdirAll(name string, mode os.FileMode) error {
	if name == "." {
		return nil
	}
	if err := r.guard(name, true); err != nil {
		return err
	}
	parts := strings.Split(name, "/")
	for n := 1; n <= len(parts); n++ {
		current := strings.Join(parts[:n], "/")
		info, err := r.Lstat(current)
		if os.IsNotExist(err) {
			if err = r.backend.mkdir(current, mode); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = r.Lstat(current)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("not a real directory: %s", current)
		}
	}
	return nil
}
func (r *Root) WriteAtomic(name string, body []byte, mode os.FileMode) error {
	if err := r.guard(name, true); err != nil {
		return err
	}
	if err := r.MkdirAll(path.Dir(name), 0700); err != nil {
		return err
	}
	id, err := model.ID()
	if err != nil {
		return err
	}
	temporary := path.Join(path.Dir(name), ".workcopy-tmp-"+id)
	file, err := r.backend.create(temporary, mode)
	if err != nil {
		return err
	}
	// The temporary file is removed on every failure path. After a successful
	// rename it no longer exists, and a remove would only cost a wasted round
	// trip per file on filesystems with slow metadata calls.
	renamed := false
	defer func() {
		if !renamed {
			r.backend.remove(temporary)
		}
	}()
	if _, err = file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = r.guard(name, true); err != nil {
		return err
	}
	if err = r.backend.rename(temporary, name); err != nil {
		return err
	}
	renamed = true
	return nil
}

// ExecutableBitsFaithful reports whether the filesystem stores POSIX
// permission bits honestly, judged by a file Workcopy itself created without
// any execute bit (mode 0600). Some filesystems, notably WSL's drvfs/9p mounts
// of Windows drives, FAT/exFAT and some network shares, report every file as
// executable, which would make every file look locally modified.
//
// known is false when the probe file is absent or not a regular file; callers
// should then keep their declared setting.
func (r *Root) ExecutableBitsFaithful(probe string) (faithful, known bool) {
	info, err := r.Lstat(probe)
	if err != nil || !info.Mode().IsRegular() {
		return false, false
	}
	return info.Mode().Perm()&0111 == 0, true
}
func (r *Root) Remove(name string) error {
	if err := r.guard(name, true); err != nil {
		return err
	}
	return r.backend.remove(name)
}
func (r *Root) Symlink(name, target string, external bool) error {
	if err := r.guard(name, true); err != nil {
		return err
	}
	if !external {
		if path.IsAbs(target) || strings.Contains(target, "\\") {
			return fmt.Errorf("unsafe source link target")
		}
		if err := ValidateSourcePath(path.Clean(path.Join(path.Dir(name), target))); err != nil {
			return err
		}
	}
	if err := r.MkdirAll(path.Dir(name), 0700); err != nil {
		return err
	}
	return r.backend.symlink(target, name)
}

// ProbeSymlink reports whether a symbolic link can be created beneath the root.
// Windows needs Developer Mode or the "create symbolic links" privilege, and
// some volumes (FAT, exFAT) never support them. The probe link is removed
// before returning and is never followed.
func (r *Root) ProbeSymlink() error {
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	name := ".workcopy-symlink-probe-" + hex.EncodeToString(suffix[:])
	if err := r.Symlink(name, "workcopy-probe-target", true); err != nil {
		return err
	}
	return r.Remove(name)
}
func (r *Root) Walk(fn fs.WalkDirFunc) error {
	if err := r.backend.verify(); err != nil {
		return err
	}
	return fs.WalkDir(r.backend.filesystem(), ".", fn)
}

// WalkFrom walks one directory (or file) beneath the root. Symbolic links are
// reported, never followed. A missing start is not an error: it yields nothing.
func (r *Root) WalkFrom(start string, fn fs.WalkDirFunc) error {
	if err := r.guard(start, true); err != nil {
		return err
	}
	if _, err := r.backend.lstat(start); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return fs.WalkDir(r.backend.filesystem(), start, fn)
}

func (r *Root) OpenLock(name string) (*os.File, error) {
	if err := r.guard(name, true); err != nil {
		return nil, err
	}
	if err := r.MkdirAll(path.Dir(name), 0700); err != nil {
		return nil, err
	}
	info, err := r.Lstat(name)
	if os.IsNotExist(err) {
		file, createErr := r.backend.create(name, 0600)
		if createErr == nil {
			return file, nil
		}
		if !os.IsExist(createErr) {
			return nil, createErr
		}
		info, err = r.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("invalid lock file")
	}
	file, err := r.backend.openRead(name)
	if err != nil {
		return nil, err
	}
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) {
		file.Close()
		return nil, fmt.Errorf("lock identity changed")
	}
	return file, nil
}
func (r *Root) ReplaceSymlink(name, target string, external bool) error {
	if err := r.guard(name, true); err != nil {
		return err
	}
	if !external {
		if path.IsAbs(target) || strings.Contains(target, "\\") {
			return fmt.Errorf("unsafe source link target")
		}
		if err := ValidateSourcePath(path.Clean(path.Join(path.Dir(name), target))); err != nil {
			return err
		}
	}
	if err := r.MkdirAll(path.Dir(name), 0700); err != nil {
		return err
	}
	id, err := model.ID()
	if err != nil {
		return err
	}
	tmp := path.Join(path.Dir(name), ".workcopy-tmp-"+id)
	if err = r.backend.symlink(target, tmp); err != nil {
		return err
	}
	defer r.backend.remove(tmp)
	if err = r.guard(name, true); err != nil {
		return err
	}
	return r.backend.rename(tmp, name)
}

func (r *Root) Rename(old, new string) error {
	if err := r.guard(old, true); err != nil {
		return err
	}
	if err := r.guard(new, true); err != nil {
		return err
	}
	if _, err := r.Lstat(new); err == nil {
		return fmt.Errorf("rename destination exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	return r.backend.rename(old, new)
}
