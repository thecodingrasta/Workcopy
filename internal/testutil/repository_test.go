package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceRootFromDirectory(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "internal", "testutil")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(root, "go.mod")
	for _, declaration := range []string{"module workcopy\n", "module workcopy\r\n"} {
		if err := os.WriteFile(module, []byte(declaration), 0600); err != nil {
			t.Fatal(err)
		}
		found, err := sourceRootFromDirectory(directory)
		if err != nil || found != root {
			t.Fatalf("source root: %q, %v", found, err)
		}
	}
	if err := os.WriteFile(module, []byte("module unrelated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceRootFromDirectory(directory); err == nil {
		t.Fatal("accepted unrelated module")
	}
}
