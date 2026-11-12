package fsops

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
)

// copyBuffer is the unit streamed files move in. It bounds memory per file.
const copyBuffer = 1 << 20

// OpenRegular opens a file the caller has just examined with Lstat, checking
// that what was opened is the same regular file (not swapped for a link or
// another file in between). The caller closes it.
func (r *Root) OpenRegular(name string, info os.FileInfo) (*os.File, error) {
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", name)
	}
	file, err := r.backend.openRead(name)
	if err != nil {
		return nil, err
	}
	actual, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		file.Close()
		return nil, fmt.Errorf("file changed while opening: %s", name)
	}
	return file, nil
}

// HashRegular returns the SHA-256 and length of a file, reading it in fixed-size
// pieces so a file of any size costs the same memory. info must come from
// r.Lstat(name).
func (r *Root) HashRegular(name string, info os.FileInfo) (string, int64, error) {
	file, err := r.OpenRegular(name, info)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hasher := sha256.New()
	written, err := io.CopyBuffer(hasher, file, make([]byte, copyBuffer))
	if err != nil {
		return "", 0, err
	}
	if written != info.Size() {
		return "", 0, fmt.Errorf("file changed size while reading: %s", name)
	}
	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}

// WriteAtomicFrom writes a file of known size and hash from a producer that
// streams its bytes, with the guarantees of WriteAtomic: nothing appears at the
// final name until the content is complete, flushed and verified against
// wantSHA256 and wantSize, and a failure leaves no temporary file behind.
func (r *Root) WriteAtomicFrom(name string, mode os.FileMode, wantSize int64, wantSHA256 string, produce func(io.Writer) error) error {
	if err := r.guard(name, true); err != nil {
		return err
	}
	if err := r.MkdirAll(path.Dir(name), 0700); err != nil {
		return err
	}
	id, err := newTempID()
	if err != nil {
		return err
	}
	temporary := path.Join(path.Dir(name), TempPrefix+id)
	file, err := r.backend.create(temporary, mode)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			r.backend.remove(temporary)
		}
	}()
	hasher := sha256.New()
	counter := &countingWriter{}
	if err = produce(io.MultiWriter(file, hasher, counter)); err != nil {
		file.Close()
		return err
	}
	if counter.n != wantSize || hex.EncodeToString(hasher.Sum(nil)) != wantSHA256 {
		file.Close()
		return fmt.Errorf("streamed content does not match its recorded hash or size: %s", name)
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

type countingWriter struct{ n int64 }

func (w *countingWriter) Write(p []byte) (int, error) { w.n += int64(len(p)); return len(p), nil }
