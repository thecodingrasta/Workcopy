package fsops

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathValidation(t *testing.T) {
	for _, name := range []string{"../x", "/x", "C:/x", "a/../../x", "a\\b", ".git/config", ".workcopy/x", "a/.git/x", "a/CON.txt", "a./b", "a:b", "a\nfile", "a//b", ""} {
		if ValidateSourcePath(name) == nil {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"a/file.txt", "src/a b.cs", "README.md", ".env.example"} {
		if err := ValidateSourcePath(name); err != nil {
			t.Fatal(name, err)
		}
	}
}
func TestRejectsSymlinkParentsAndDoesNotTouchTarget(t *testing.T) {
	d := t.TempDir()
	outside := t.TempDir()
	root, err := OpenRoot(d)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = os.Symlink(outside, filepath.Join(d, "link")); err != nil {
		t.Skip("symlink permission unavailable:", err)
	}
	if err = root.WriteAtomic("link/escape", []byte("bad"), 0600); err == nil {
		t.Fatal("followed parent symlink")
	}
	if _, err = os.Stat(filepath.Join(outside, "escape")); !os.IsNotExist(err) {
		t.Fatal("outside file written")
	}
}
func TestAtomicReplacementDoesNotEditHardlinkTarget(t *testing.T) {
	d := t.TempDir()
	target := filepath.Join(t.TempDir(), "source")
	os.WriteFile(target, []byte("old"), 0600)
	if err := os.Link(target, filepath.Join(d, "file")); err != nil {
		t.Skip(err)
	}
	root, _ := OpenRoot(d)
	defer root.Close()
	if err := root.WriteAtomic("file", []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "old" {
		t.Fatal("shared inode modified")
	}
}
func TestRejectsInvalidUTF8Paths(t *testing.T) {
	if ValidateSourcePath(string([]byte{'a', 0xff})) == nil {
		t.Fatal("path cannot round-trip through JSON")
	}
}

func TestRejectsWindowsSuperscriptDevices(t *testing.T) {
	for _, name := range []string{"COM¹", "LPT².txt", "COM³/file"} {
		if ValidateSourcePath(name) == nil {
			t.Errorf("accepted Windows device path %q", name)
		}
	}
}
