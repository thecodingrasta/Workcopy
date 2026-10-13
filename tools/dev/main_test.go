package main

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatPreservesPrivateAndVendoredGo(t *testing.T) {
	root := t.TempDir()
	private := []byte("package dependency\nfunc unchanged( ){ }\n")
	for _, directory := range []string{"vendor", ".cache", ".codex", ".gitnexus", "node_modules"} {
		path := filepath.Join(root, directory)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "unchanged.go"), private, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, write := range []bool{false, true} {
		if err := formatFiles(root, write); err != nil {
			t.Fatal(err)
		}
	}
	for _, directory := range []string{"vendor", ".cache", ".codex", ".gitnexus", "node_modules"} {
		body, err := os.ReadFile(filepath.Join(root, directory, "unchanged.go"))
		if err != nil || string(body) != string(private) {
			t.Fatalf("formatter changed excluded input %s: %v", directory, err)
		}
	}
	public := filepath.Join(root, "public.go")
	if err := os.WriteFile(public, private, 0600); err != nil {
		t.Fatal(err)
	}
	if err := formatFiles(root, false); err == nil {
		t.Fatal("unformatted project source accepted")
	}
	if err := formatFiles(root, true); err != nil {
		t.Fatal(err)
	}
	if err := formatFiles(root, false); err != nil {
		t.Fatal(err)
	}
}

func TestSourceRootAcceptsLFAndCRLFModuleDeclarations(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(fmtEnding(ending), func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.WriteFile("go.mod", []byte("module workcopy"+ending+ending+"go 1.25"+ending), 0600); err != nil {
				t.Fatal(err)
			}
			err := run(context.Background(), []string{"unknown"})
			if err == nil || !strings.Contains(err.Error(), "unknown task") {
				t.Fatalf("valid module declaration rejected: %v", err)
			}
		})
	}
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", []byte("module workcopy-other\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"unknown"}); err == nil || !strings.Contains(err.Error(), "source root") {
		t.Fatalf("wrong module accepted: %v", err)
	}
}

func fmtEnding(ending string) string {
	if ending == "\r\n" {
		return "CRLF"
	}
	return "LF"
}

func TestBuildDirectoryRejectsSymlinkAndUnownedContents(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, "dist")); err == nil {
		if err = prepareDist(root); err == nil {
			t.Fatal("followed dist symlink")
		}
		os.Remove(filepath.Join(root, "dist"))
	}
	os.Mkdir(filepath.Join(root, "dist"), 0700)
	os.WriteFile(filepath.Join(root, "dist", "valuable"), []byte("keep"), 0600)
	if err := prepareDist(root); err == nil {
		t.Fatal("adopted unowned output directory")
	}
}
func TestReleaseVersionRejectsShellAndPathSyntax(t *testing.T) {
	for _, value := range []string{"../v1.0.0", "v1.0.0;whoami", "main", "v1.0", "v1.0.0 + flags"} {
		if validVersion(value) {
			t.Error("accepted", value)
		}
	}
	for _, value := range []string{"v0.1.0", "v1.2.3-rc.1"} {
		if !validVersion(value) {
			t.Error("rejected", value)
		}
	}
}
func TestReleaseZipRetainsContentsAndExecutableMode(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "asset.zip")
	entries := []archiveEntry{{Name: "workcopy", Data: []byte("executable"), Mode: 0755}, {Name: "LICENSE", Data: []byte("notice"), Mode: 0644}}
	if err := writeArchive(filename, entries); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if len(reader.File) != 2 || reader.File[0].Mode().Perm() != 0755 {
		t.Fatal("archive lost files or executable mode")
	}
	if err = writeArchive(filename, entries); err == nil {
		t.Fatal("existing release asset was overwritten")
	}
}
func TestFormattingRejectsLinkedSource(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(t.TempDir(), "other.go")
	os.WriteFile(target, []byte("package other\n"), 0600)
	if err := os.Symlink(target, filepath.Join(directory, "linked.go")); err != nil {
		t.Skip(err)
	}
	if err := formatFiles(directory, true); err == nil {
		t.Fatal("formatter accepted a source symlink")
	}
}

func TestCorrespondingSourceExcludesPrivateAndGeneratedFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"go.mod", "go.sum", "LICENSE", "COPYRIGHT", "README.md", ".gitignore", ".editorconfig", ".gitattributes", "cmd/workcopy/main.go", "tools/dev/main.go", "docs/nested/guide.md", ".codex/private.md", ".cache/secret.go", ".git/config", "prebuilt/archive.zip", "dist/workcopy.exe", "unrelated.txt"} {
		filename := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := sourceFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, entry := range entries {
		names[entry.Name] = true
	}
	for _, name := range []string{"go.mod", "COPYRIGHT", ".editorconfig", ".gitattributes", "tools/dev/main.go", "docs/nested/guide.md"} {
		if !names[name] {
			t.Error("source input omitted", name)
		}
	}
	for _, name := range []string{".codex/private.md", ".cache/secret.go", ".git/config", "prebuilt/archive.zip", "dist/workcopy.exe", "unrelated.txt"} {
		if names[name] {
			t.Error("private/generated file included", name)
		}
	}
}
