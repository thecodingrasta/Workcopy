package fsops

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func sum(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }

func patterned(size int) []byte {
	out := make([]byte, size)
	state := uint32(2463534242)
	for i := range out {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		out[i] = byte(state)
	}
	return out
}

func TestHashRegularMatchesInMemoryHashAtEverySizeBoundary(t *testing.T) {
	directory := t.TempDir()
	root, err := OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// Around the copy buffer, empty, and a few multiples, so partial final reads are covered.
	for _, size := range []int{0, 1, copyBuffer - 1, copyBuffer, copyBuffer + 1, 3*copyBuffer + 17} {
		data := patterned(size)
		name := "f.bin"
		if err = os.WriteFile(filepath.Join(directory, name), data, 0644); err != nil {
			t.Fatal(err)
		}
		info, err := root.Lstat(name)
		if err != nil {
			t.Fatal(err)
		}
		got, n, err := root.HashRegular(name, info)
		if err != nil || got != sum(data) || n != int64(size) {
			t.Errorf("size %d: got %s,%d,%v want %s,%d", size, got, n, err, sum(data), size)
		}
	}
}

func TestHashRegularRefusesWhatIsNotTheStatedFile(t *testing.T) {
	directory := t.TempDir()
	root, err := OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = os.WriteFile(filepath.Join(directory, "a"), []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	info, _ := root.Lstat("a")
	// The file grows after it was examined: the length no longer matches.
	if err = os.WriteFile(filepath.Join(directory, "a"), []byte("one and more"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err = root.HashRegular("a", info); err == nil {
		t.Error("a file that changed size after Lstat must be refused")
	}
	if err = os.Mkdir(filepath.Join(directory, "d"), 0755); err != nil {
		t.Fatal(err)
	}
	dirInfo, _ := root.Lstat("d")
	if _, _, err = root.HashRegular("d", dirInfo); err == nil {
		t.Error("a directory is not a regular file")
	}
}

func TestWriteAtomicFromStreamsVerifiesAndLeavesNothingOnFailure(t *testing.T) {
	directory := t.TempDir()
	root, err := OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	data := patterned(2*copyBuffer + 5)
	feed := func(w io.Writer) error { _, e := io.Copy(w, bytes.NewReader(data)); return e }

	if err = root.WriteAtomicFrom("sub/dir/big.bin", 0644, int64(len(data)), sum(data), feed); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(directory, "sub", "dir", "big.bin")); !bytes.Equal(got, data) {
		t.Fatal("streamed file differs from its source")
	}

	cases := map[string]struct {
		size    int64
		hash    string
		produce func(io.Writer) error
	}{
		"wrong hash":     {int64(len(data)), sum([]byte("other")), feed},
		"wrong size":     {int64(len(data)) + 1, sum(data), feed},
		"producer error": {int64(len(data)), sum(data), func(w io.Writer) error { w.Write(data[:100]); return errors.New("source failed midway") }},
	}
	for label, c := range cases {
		target := "failed-" + label + ".bin"
		if err = root.WriteAtomicFrom(target, 0644, c.size, c.hash, c.produce); err == nil {
			t.Errorf("%s: must fail", label)
		}
		if _, statErr := os.Lstat(filepath.Join(directory, target)); !os.IsNotExist(statErr) {
			t.Errorf("%s: nothing may appear at the final name: %v", label, statErr)
		}
	}
	entries, _ := os.ReadDir(directory)
	for _, entry := range entries {
		if IsTempArtifact(entry.Name()) {
			t.Errorf("a failed write left a temporary file: %s", entry.Name())
		}
	}
}

func TestWriteAtomicFromReplacesAnExistingFileOnlyOnSuccess(t *testing.T) {
	directory := t.TempDir()
	root, err := OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = os.WriteFile(filepath.Join(directory, "keep.bin"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	bad := func(w io.Writer) error { w.Write([]byte("partial")); return errors.New("cut off") }
	if err = root.WriteAtomicFrom("keep.bin", 0644, 7, sum([]byte("partial")), bad); err == nil {
		t.Fatal("must fail")
	}
	if got, _ := os.ReadFile(filepath.Join(directory, "keep.bin")); string(got) != "original" {
		t.Fatalf("a failed replacement must leave the original untouched, got %q", got)
	}
}

func TestOpenRegularRefusesALinkSwappedInAfterLstat(t *testing.T) {
	directory := t.TempDir()
	outside := t.TempDir()
	root, err := OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	secret := filepath.Join(outside, "secret")
	if err = os.WriteFile(secret, []byte("outside the lane"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "f"), []byte("inside"), 0644); err != nil {
		t.Fatal(err)
	}
	info, _ := root.Lstat("f")
	if err = os.Remove(filepath.Join(directory, "f")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(secret, filepath.Join(directory, "f")); err != nil {
		t.Skip("symlink permission unavailable:", err)
	}
	if file, err := root.OpenRegular("f", info); err == nil {
		file.Close()
		t.Fatal("a file replaced by a link between Lstat and open must not be opened")
	}
}
