package state

import (
	"os"
	"path/filepath"
	"testing"
	"workcopy/internal/fsops"
	"workcopy/internal/model"
)

func TestNativeJunctionIsReadWithoutTraversingItsTarget(t *testing.T) {
	root, err := fsops.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	target := t.TempDir()
	if err = os.WriteFile(filepath.Join(target, "sentinel"), []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = root.ReplaceDirectoryLink("packages", target); err != nil {
		t.Fatal(err)
	}
	version, body, err := ReadVersion(root, "packages", model.FileVersion{}, false)
	if err != nil || version.Kind != model.Symlink || string(body) != filepath.Clean(target) {
		t.Fatalf("junction version: %+v %q %v", version, body, err)
	}
	if err = root.Remove("packages"); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(filepath.Join(target, "sentinel"))
	if err != nil || string(body) != "untouched" {
		t.Fatal("shared target changed", err)
	}
}
