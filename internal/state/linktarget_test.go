package state

import (
	"testing"

	"workcopy/internal/model"
)

// Windows returns a relative link target with backslashes however it was
// written, but Git and Workcopy's baseline hold the slash form; a mapping to a
// shared directory is absolute and must be compared exactly as Windows reports
// it. The Windows CI job exercises the real round trip; this pins the rule on
// every platform.
func TestCanonicalLinkTarget(t *testing.T) {
	cases := []struct{ name, platform, target, want string }{
		{"relative target read back by Windows", "windows", `..\bin\run.sh`, "../bin/run.sh"},
		{"already slashed", "windows", "bin/run.sh", "bin/run.sh"},
		{"mixed separators", "windows", `a/b\c`, "a/b/c"},
		{"single component", "windows", "target.txt", "target.txt"},
		{"drive path of a mapping", "windows", `C:\cache\deps`, `C:\cache\deps`},
		{"lower-case drive", "windows", `d:\x\y`, `d:\x\y`},
		{"UNC path of a mapping", "windows", `\\server\share\deps`, `\\server\share\deps`},
		{"NT namespace junction target", "windows", `\??\C:\cache\deps`, `\??\C:\cache\deps`},
		{"rooted without drive", "windows", `\cache\deps`, `\cache\deps`},
		{"slash rooted", "windows", "/cache/deps", "/cache/deps"},
		{"empty", "windows", "", ""},
		{"backslash is an ordinary character on Linux", "linux", `a\b`, `a\b`},
		{"backslash is an ordinary character on macOS", "darwin", `..\x`, `..\x`},
	}
	for _, c := range cases {
		if got := canonicalLinkTarget(c.target, c.platform); got != c.want {
			t.Errorf("%s: canonicalLinkTarget(%q, %q) = %q, want %q", c.name, c.target, c.platform, got, c.want)
		}
	}
}

func TestSplitLinkWritesPutsLinksAfterEverythingTheyMayPointAt(t *testing.T) {
	file := func(path string) Operation {
		return Operation{Path: path, After: model.Version(model.Regular, 0644, []byte(path))}
	}
	link := func(path string) Operation {
		return Operation{Path: path, After: model.Version(model.Symlink, 0120000, []byte("target"))}
	}
	// A link that sorts before the directory it points at, as in a repository
	// that tracks "current -> releases".
	files, links := splitLinkWrites([]Operation{link("a-current"), file("b.txt"), link("m-link"), file("releases/v1/app.txt"), file("z.txt")})
	got := func(operations []Operation) string {
		names := ""
		for _, op := range operations {
			names += op.Path + ";"
		}
		return names
	}
	if got(files) != "b.txt;releases/v1/app.txt;z.txt;" || got(links) != "a-current;m-link;" {
		t.Fatalf("files %q, links %q", got(files), got(links))
	}
	if files, links = splitLinkWrites(nil); len(files) != 0 || len(links) != 0 {
		t.Fatal("nothing in, nothing out")
	}
}
