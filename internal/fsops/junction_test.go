package fsops

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestJunctionBufferContainsByteOffsetsAndTerminators(t *testing.T) {
	body, err := junctionBuffer(`C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(body[:4]) != 0xa0000003 {
		t.Fatal("wrong reparse tag")
	}
	if int(binary.LittleEndian.Uint16(body[4:6])) != len(body)-8 {
		t.Fatal("wrong data length")
	}
	subLength := int(binary.LittleEndian.Uint16(body[10:12]))
	printOffset := int(binary.LittleEndian.Uint16(body[12:14]))
	if printOffset != subLength+2 {
		t.Fatal("missing substitute terminator")
	}
	if _, err = junctionBuffer(`\\server\share`); err == nil {
		t.Fatal("UNC junction target should be rejected")
	}
}
func TestDirectoryLinkDoesNotCopyTarget(t *testing.T) {
	directory := t.TempDir()
	target := t.TempDir()
	os.WriteFile(filepath.Join(target, "marker"), []byte("cache"), 0600)
	root, _ := OpenRoot(directory)
	defer root.Close()
	if err := root.ReplaceDirectoryLink("packages", target); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(directory, "packages"))
	if err != nil || filepath.Clean(link) != filepath.Clean(target) {
		t.Fatal(link, err)
	}
	if err = root.Remove("packages"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(target, "marker"))
	if err != nil || string(b) != "cache" {
		t.Fatal("target changed")
	}
}

func TestDirectoryLinkAcceptsForwardSlashTarget(t *testing.T) {
	directory := t.TempDir()
	target := t.TempDir()
	root, err := OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = root.ReplaceDirectoryLink("packages", filepath.ToSlash(target)); err != nil {
		t.Fatal(err)
	}
	if err = root.Remove("packages"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(target); err != nil {
		t.Fatalf("target removed with link: %v", err)
	}
}
