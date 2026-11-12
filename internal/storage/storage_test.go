package storage

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func walkDir(root string) Walker {
	return func(fn fs.WalkDirFunc) error { return fs.WalkDir(os.DirFS(root), ".", fn) }
}

func measure(t *testing.T, root string, classify Classifier) Account {
	t.Helper()
	if classify == nil {
		classify = func(string) Class { return "all" }
	}
	account, err := Measure(context.Background(), root, walkDir(root), classify)
	if err != nil {
		t.Fatal(err)
	}
	return account
}

// A file with two names inside the tree is two names but one allocation, and
// deleting the tree frees it.
func TestHardLinksInsideTheTreeAreCountedOnce(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.bin"), 20000)
	write(t, filepath.Join(root, "other.bin"), 20000)
	if err := os.Link(filepath.Join(root, "a.bin"), filepath.Join(root, "b.bin")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	account := measure(t, root, nil)
	if account.Total.Files != 3 || account.Total.LinkedFiles != 2 {
		t.Fatalf("names=%d linked=%d, want 3 and 2", account.Total.Files, account.Total.LinkedFiles)
	}
	if account.Total.LogicalBytes != 60000 {
		t.Fatalf("logical %d, want 60000 (per name)", account.Total.LogicalBytes)
	}
	// Two allocations, not three: the linked pair is one file.
	if account.Total.AllocatedBytes >= 3*20000 || account.Total.AllocatedBytes < 2*20000 {
		t.Fatalf("allocated %d, want between 40000 and 60000 (two files)", account.Total.AllocatedBytes)
	}
	if account.Total.ReclaimableBytes != account.Total.AllocatedBytes || account.Total.SharedBytes != 0 {
		t.Fatalf("reclaimable %d shared %d: every name is inside the tree, so all of it is reclaimable", account.Total.ReclaimableBytes, account.Total.SharedBytes)
	}
}

// A name outside the tree keeps the bytes alive, so they are shared, not
// reclaimable. Removing the outside name must flip the answer (negative control).
func TestFileAlsoLinkedOutsideTheTreeIsSharedNotReclaimable(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "lane")
	outside := filepath.Join(parent, "store", "blob.bin")
	write(t, outside, 30000)
	write(t, filepath.Join(root, "own.bin"), 10000)
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(root, "node_modules", "blob.bin")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	classify := func(relative string) Class {
		if strings.HasPrefix(relative, "node_modules/") {
			return "linked"
		}
		return "own"
	}
	account := measure(t, root, classify)
	linked, own := account.Classes["linked"], account.Classes["own"]
	if linked == nil || own == nil {
		t.Fatalf("classes missing: %+v", account.Classes)
	}
	if linked.SharedBytes == 0 || linked.ReclaimableBytes != 0 {
		t.Fatalf("outside-linked file: shared=%d reclaimable=%d, want shared>0 and reclaimable=0", linked.SharedBytes, linked.ReclaimableBytes)
	}
	if own.ReclaimableBytes == 0 || own.SharedBytes != 0 {
		t.Fatalf("private file: shared=%d reclaimable=%d", own.SharedBytes, own.ReclaimableBytes)
	}
	// Negative control: drop the outside name and the same bytes become reclaimable.
	if err := os.Remove(outside); err != nil {
		t.Fatal(err)
	}
	after := measure(t, root, classify).Classes["linked"]
	if after.SharedBytes != 0 || after.ReclaimableBytes == 0 {
		t.Fatalf("after removing the outside name: shared=%d reclaimable=%d, want shared=0 and reclaimable>0", after.SharedBytes, after.ReclaimableBytes)
	}
}

// linkDirectory links name to target the way Workcopy's share does: a junction
// on Windows (no privilege needed) and a symbolic link elsewhere.
func linkDirectory(t *testing.T, name, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		if output, err := exec.Command("cmd", "/c", "mklink", "/J", name, target).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J: %v: %s", err, output)
		}
		return
	}
	if err := os.Symlink(target, name); err != nil {
		t.Fatal(err)
	}
}

// A link to a directory must be listed and never entered or sized, even when it
// points at a large directory. The control proves the walk would have found the
// big file had it followed the link.
func TestDirectoryLinkIsNotEntered(t *testing.T) {
	parent := t.TempDir()
	big := filepath.Join(parent, "big")
	write(t, filepath.Join(big, "huge.bin"), 500000)
	root := filepath.Join(parent, "lane")
	write(t, filepath.Join(root, "small.txt"), 100)
	linkDirectory(t, filepath.Join(root, "linked"), big)
	account := measure(t, root, nil)
	if account.Total.Files != 1 || account.Total.LogicalBytes != 100 {
		t.Fatalf("followed the link: files=%d logical=%d", account.Total.Files, account.Total.LogicalBytes)
	}
	if len(account.Links) != 1 || account.Links[0] != "linked" {
		t.Fatalf("link not reported: %v", account.Links)
	}
	control := measure(t, big, nil)
	if control.Total.LogicalBytes != 500000 {
		t.Fatalf("control: measuring the target directly saw %d bytes, want 500000", control.Total.LogicalBytes)
	}
}

func TestCancellationReturnsNoAccount(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a"), 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	account, err := Measure(ctx, root, walkDir(root), func(string) Class { return "all" })
	if err == nil {
		t.Fatal("cancelled measurement returned success")
	}
	if account.Total.Files != 0 {
		t.Fatalf("partial account returned: %+v", account.Total)
	}
}

// Classification decides the row; the same file is never counted in two rows.
func TestEveryFileLandsInExactlyOneClass(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "a.go"), 1000)
	write(t, filepath.Join(root, "bin", "a.dll"), 2000)
	write(t, filepath.Join(root, "bin", "b.dll"), 3000)
	classify := func(relative string) Class {
		if strings.HasPrefix(relative, "bin/") {
			return "output"
		}
		return "source"
	}
	account := measure(t, root, classify)
	if account.Classes["output"].Files != 2 || account.Classes["output"].LogicalBytes != 5000 || account.Classes["source"].Files != 1 {
		t.Fatalf("classes wrong: %+v / %+v", account.Classes["output"], account.Classes["source"])
	}
	if account.Total.Files != 3 || account.Total.LogicalBytes != 6000 {
		t.Fatalf("total %+v is not the sum of the classes", account.Total)
	}
}

// The biggest consumers are named by top-level directory and class, ordered by
// allocation, so a full disk can be traced to a place. Bytes of one file are
// credited once even when two names point at it.
func TestLargestDirectoriesAreOrderedAndCreditedOnce(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "cache", "ab", "x"), 90000)
	write(t, filepath.Join(root, "cache", "cd", "y"), 90000)
	write(t, filepath.Join(root, "src", "a.go"), 20000)
	write(t, filepath.Join(root, "top.txt"), 5000)
	if err := os.Link(filepath.Join(root, "src", "a.go"), filepath.Join(root, "src", "alias.go")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	classify := func(relative string) Class {
		if strings.HasPrefix(relative, "cache/") {
			return "other"
		}
		return "source"
	}
	account := measure(t, root, classify)
	if len(account.Largest) < 3 {
		t.Fatalf("largest: %+v", account.Largest)
	}
	first := account.Largest[0]
	if first.Path != "cache" || first.Class != "other" || first.Files != 2 || first.AllocatedBytes < 180000 {
		t.Fatalf("largest first = %+v, want cache/other with both files", first)
	}
	for i := 1; i < len(account.Largest); i++ {
		if account.Largest[i].AllocatedBytes > account.Largest[i-1].AllocatedBytes {
			t.Fatalf("not ordered by allocation: %+v", account.Largest)
		}
	}
	var src, rootRow *DirectoryTotal
	for i := range account.Largest {
		switch account.Largest[i].Path {
		case "src":
			src = &account.Largest[i]
		case ".":
			rootRow = &account.Largest[i]
		}
	}
	if src == nil || src.Files != 2 || src.AllocatedBytes >= 2*20000 {
		t.Fatalf("src should list both names but credit one allocation: %+v", src)
	}
	if rootRow == nil || rootRow.Files != 1 {
		t.Fatalf("files at the top of the tree belong to %q: %+v", ".", account.Largest)
	}
}

func TestAllocationIsReportedForARealFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	write(t, path, 100000)
	facts, err := Facts(path)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Logical != 100000 || facts.Allocated <= 0 || facts.Links != 1 {
		t.Fatalf("facts %+v", facts)
	}
	other := filepath.Join(filepath.Dir(path), "g")
	write(t, other, 100000)
	second, err := Facts(other)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == facts.ID {
		t.Fatal("two different files share an identity")
	}
}

func TestVolumeOfATemporaryDirectory(t *testing.T) {
	dir := t.TempDir()
	volume, err := Describe(dir)
	if err != nil {
		t.Fatal(err)
	}
	if volume.ID == "" || volume.FileSystem == "" || volume.TotalBytes == 0 || volume.FreeBytes > volume.TotalBytes {
		t.Fatalf("volume %+v", volume)
	}
	again, err := Describe(filepath.Join(dir, "does", "not", "exist"))
	if err != nil {
		t.Fatalf("a missing path must describe its nearest existing ancestor: %v", err)
	}
	if !SameVolume(volume, again) || again.Path != volume.Path {
		t.Fatalf("missing child resolved to %+v, want the volume of %s", again, dir)
	}
	if SameVolume(volume, Volume{}) {
		t.Fatal("an empty volume must not match any volume")
	}
	if runtime.GOOS == "windows" && volume.FileSystem == "NTFS" && (volume.HardLinks != Yes || volume.BlockClone != No) {
		t.Fatalf("NTFS capabilities wrong: %+v", volume)
	}
}

func TestToolCachesPreferEnvironmentThenDocumentedDefault(t *testing.T) {
	env := map[string]string{"NUGET_PACKAGES": `/n/pkgs`, "GOCACHE": `/g/cache`, "GOPATH": "/gp" + string(os.PathListSeparator) + "/other"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	caches := ToolCaches(lookup, "/home/u", "/home/u/.cache")
	got := map[string]CacheLocation{}
	for _, c := range caches {
		got[c.Tool] = c
	}
	if got["nuget packages"].Path != `/n/pkgs` || got["nuget packages"].Source != "NUGET_PACKAGES" {
		t.Fatalf("nuget: %+v", got["nuget packages"])
	}
	if got["go build cache"].Path != `/g/cache` {
		t.Fatalf("go build cache: %+v", got["go build cache"])
	}
	if want := filepath.Join("/gp", "pkg", "mod"); got["go module cache"].Path != want {
		t.Fatalf("go module cache %q, want first GOPATH entry %q", got["go module cache"].Path, want)
	}
	defaults := ToolCaches(func(string) (string, bool) { return "", false }, "/home/u", "/home/u/.cache")
	for _, c := range defaults {
		if !strings.Contains(c.Source, "default location") {
			t.Fatalf("a default must say so: %+v", c)
		}
	}
	if len(defaults) != 3 {
		t.Fatalf("defaults: %+v", defaults)
	}
	if len(ToolCaches(func(string) (string, bool) { return "", false }, "", "")) != 0 {
		t.Fatal("guessed a location with no home directory")
	}
}
