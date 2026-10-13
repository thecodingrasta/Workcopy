package fsops

import (
	"path"
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzPortablePathValidation(f *testing.F) {
	for _, seed := range []string{"src/main.go", "../escape", "src/../../escape", "C:/drive", "aux.txt", "CON", "dir/link/file", "file with spaces.txt", "a//b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if ValidateRelative(name) != nil {
			return
		}
		if !utf8.ValidString(name) || name == "" || name == "." || path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, "\\") {
			t.Fatalf("accepted unsafe path: %q", name)
		}
		for _, part := range strings.Split(name, "/") {
			if part == ".." || part == "" {
				t.Fatalf("accepted escaping path: %q", name)
			}
		}
	})
}
