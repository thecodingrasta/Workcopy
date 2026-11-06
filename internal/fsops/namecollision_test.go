package fsops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// volumeEquates asks the volume under directory whether two spellings are one
// entry. The tests compare FindNameCollision with this, so they hold on every
// platform: macOS equates canonically equivalent Unicode, Windows equates case,
// Linux equates neither.
func volumeEquates(t *testing.T, directory, made, looked string) bool {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(directory, filepath.FromSlash(made)), 0700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Join(directory, strings.Split(made, "/")[0]))
	_, err := os.Lstat(filepath.Join(directory, filepath.FromSlash(looked)))
	return err == nil
}

// volumeEqualsAtFirstDifference reports whether the volume treats the first
// path component at which a and b differ as one entry.
func volumeEqualsAtFirstDifference(t *testing.T, directory, a, b string) bool {
	t.Helper()
	left, right := strings.Split(a, "/"), strings.Split(b, "/")
	for index := range left {
		if left[index] != right[index] {
			return volumeEquates(t, directory, strings.Join(left[:index+1], "/"), strings.Join(right[:index+1], "/"))
		}
	}
	return true
}

func openScratch(t *testing.T) (*Root, string) {
	t.Helper()
	directory := t.TempDir()
	root, err := OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root, directory
}

func assertEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("the probe must leave nothing behind, found %v (%v)", names, err)
	}
}

func TestFindNameCollisionAgreesWithTheVolume(t *testing.T) {
	root, directory := openScratch(t)
	pairs := []struct {
		name         string
		first, other string
	}{
		{"canonically equivalent Unicode (macOS equates, Linux and Windows do not)", "docs/caf\u00e9.txt", "docs/cafe\u0301.txt"},
		{"case-only difference in non-ASCII text (Windows and macOS equate)", "docs/\u00c9cole.txt", "docs/\u00e9cole.txt"},
		{"collision in a directory component", "\u00dcn\u00ef/a.txt", "\u00fcn\u00ef/b.txt"},
	}
	for _, p := range pairs {
		// What the volume says, measured at the first component where they differ.
		want := volumeEqualsAtFirstDifference(t, directory, p.first, p.other)
		t.Logf("this volume treats %q and %q as one entry: %v", p.first, p.other, want)
		for _, order := range [][]string{{p.first, p.other}, {p.other, p.first}} {
			found, err := root.findNameCollision(append([]string{"plain.txt", "dir/ascii.txt"}, order...), true)
			if err != nil {
				t.Fatalf("%s: %v", p.name, err)
			}
			if (found != nil) != want {
				t.Errorf("%s: replay found %+v, but the volume says the spellings are the same entry: %v", p.name, found, want)
			}
			if found != nil {
				if found.Second == "" || found.First == found.Second {
					t.Errorf("%s: the collision must name two different spellings: %+v", p.name, found)
				}
			}
			assertEmpty(t, directory)
		}
	}
}

func TestFindNameCollisionQuickCheckMatchesTheVolume(t *testing.T) {
	root, directory := openScratch(t)
	names := []string{"docs/caf\u00e9.txt", "docs/cafe\u0301.txt", "ascii.txt"}
	want := volumeEquates(t, directory, "caf\u00e9", "cafe\u0301")
	found, err := root.FindNameCollision(names)
	if err != nil {
		t.Fatal(err)
	}
	if (found != nil) != want {
		t.Fatalf("FindNameCollision found %+v; the volume equates canonically equivalent spellings: %v", found, want)
	}
	assertEmpty(t, directory)
}

func TestFindNameCollisionLeavesDistinctNamesAlone(t *testing.T) {
	root, directory := openScratch(t)
	// The names of the Unicode lifecycle shape in the acceptance tests, which
	// every platform including macOS must create, plus names that share a
	// directory or a prefix with each other without being the same entry.
	names := []string{"a.txt", "docs/\u65e5\u672c\u8a9e/\u30d5\u30a1\u30a4\u30eb.txt", "docs/\u00fcn\u00ef c\u00f6d\u00e9/\u00e9.txt", "src/emoji-\U0001F600.txt", "src/\u0436.txt", "src/\u0416\u0416.txt",
		"src/\u65e5\u672c\u8a9e/\u30d5\u30a1\u30a4\u30eb.txt", "src/\u00fcn\u00ef c\u00f6d\u00e9/\u00e9.txt", "src/\u00fcn\u00ef c\u00f6d\u00e9/\u00e8.txt", "src/\u00fcn\u00ef c\u00f6d\u00e9x/\u00e9.txt",
		"docs/dir with spaces/\u00e9 b.txt", "src/semi;colon\u00e9 #hash %percent.txt", "src/caf\u00e9.txt", "src/caf\u00e8.txt", "src/cafe.txt"}
	for _, replay := range []bool{false, true} {
		found, err := root.findNameCollision(names, replay)
		if err != nil || found != nil {
			t.Fatalf("replay=%v: distinct names must not collide, got %+v (%v)", replay, found, err)
		}
		assertEmpty(t, directory)
	}
}

func TestFindNameCollisionDoesNotTouchTheVolumeWithoutNonASCIIPairs(t *testing.T) {
	root, directory := openScratch(t)
	for _, names := range [][]string{nil, {"a.txt", "b.txt"}, {"only/one/\u00e9.txt", "plain.txt"}} {
		found, err := root.FindNameCollision(names)
		if err != nil || found != nil {
			t.Fatalf("%v: %+v %v", names, found, err)
		}
		assertEmpty(t, directory)
	}
}
