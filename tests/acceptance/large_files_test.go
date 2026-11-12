package acceptance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/testutil"
)

// The report from daily use (WC-017) was a lane that could not be destroyed
// because a build had written 82 MB of XML under bin/. These tests use files over
// the real 64 MiB boundary, through the shipped executable and its default
// settings, since lowering a threshold in-process cannot show that the real
// constants are wired through.

func writeFilled(t *testing.T, name string, size int64, seed byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	block := make([]byte, 1<<20)
	for i := range block {
		block[i] = byte(i*31) ^ seed
	}
	for written := int64(0); written < size; {
		chunk := block
		if remaining := size - written; remaining < int64(len(chunk)) {
			chunk = chunk[:remaining]
		}
		if _, err = file.Write(chunk); err != nil {
			t.Fatal(err)
		}
		written += int64(len(chunk))
	}
}

func TestBuildOutputLargerThan64MiBNeverBlocksDestroy(t *testing.T) {
	t.Parallel()
	r := testutil.NewRepository(t)
	r.Write(".gitignore", "bin/\nobj/\n")
	r.Write("CTDI.Carrier/Program.cs", "class Program {}\n")
	r.Commit("project")
	owner := filepath.Join(r.Parent, "owner.json")
	if err := os.WriteFile(owner, []byte(`{"schemaVersion":1,"destroy":{"discard":["**/bin/**","**/obj/**"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(r.Parent, "lanes")
	lane := laneReport(t, r.Path, nil, 0, "create", "build", "--all", "--root", root, "--config", owner).Workspace.Path

	// The report: nine copies of an 82,100,244-byte XML file under bin/.
	for i := 0; i < 9; i++ {
		writeFilled(t, filepath.Join(lane, "CTDI.Carrier", "bin", "Debug", "net11.0", "ref"+string(rune('a'+i)), "Microsoft.Graph.xml"), 82_100_244, byte(i))
	}
	writeFilled(t, filepath.Join(lane, "CTDI.Carrier", "bin", "Debug", "playwright", "node.exe"), 88_000_000, 77)
	if err := os.MkdirAll(filepath.Join(lane, "CTDI.Carrier", "obj"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, "CTDI.Carrier", "obj", "project.assets.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	if status := laneReport(t, lane, nil, 0, "status"); len(status.Changes) != 0 {
		t.Fatalf("ignored build output is not source: %v", head(changedPaths(status)))
	}
	preview := laneReport(t, r.Path, nil, 0, "destroy", "build", "--root", root, "--dry-run")
	if effects := strings.Join(preview.Plan.Effects, "\n"); !strings.Contains(effects, "Discard 11 local-only files") {
		t.Fatalf("the plan must list the discarded build output: %s", effects)
	}
	laneReport(t, r.Path, nil, 0, "destroy", "build", "--root", root)
	if _, err := os.Lstat(lane); !os.IsNotExist(err) {
		t.Fatalf("the lane must be gone: %v", err)
	}
	if after := laneReport(t, r.Path, nil, 0, "list", "--root", root); len(after.Workspaces) != 0 {
		t.Fatalf("the lane must be unregistered: %+v", after.Workspaces)
	}
}

func TestLargeTrackedAndUntrackedFilesWorkThroughTheRealBinary(t *testing.T) {
	t.Parallel()
	r := testutil.NewRepository(t)
	r.Write("README.md", "# project\n")
	writeFilled(t, filepath.Join(r.Path, "assets", "dataset.bin"), 70<<20, 3)
	r.Commit("project with a 70 MiB tracked asset")
	root := filepath.Join(r.Parent, "lanes")

	// A tracked file over 64 MiB used to make create refuse the whole selection.
	lane := laneReport(t, r.Path, nil, 0, "create", "big", "--all", "--root", root).Workspace.Path
	assertSameBytes(t, filepath.Join(r.Path, "assets", "dataset.bin"), filepath.Join(lane, "assets", "dataset.bin"))
	if status := laneReport(t, lane, nil, 0, "status"); len(status.Changes) != 0 {
		t.Fatalf("a fresh lane must be clean: %v", head(changedPaths(status)))
	}

	// An unignored 66 MiB download used to make every command in the lane fail.
	writeFilled(t, filepath.Join(lane, "download.zip"), 66<<20, 9)
	if status := laneReport(t, lane, nil, 0, "status"); len(status.Changes) != 1 || status.Changes[0].Path != "download.zip" {
		t.Fatalf("status must list the large file as the lane's work, got %v", head(changedPaths(status)))
	}
	review := laneReport(t, lane, nil, 0, "merge")
	assertSameBytes(t, filepath.Join(lane, "download.zip"), filepath.Join(review.Review.Path, "download.zip"))
	committed := laneReport(t, lane, []string{"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.invalid"}, 0, "merge", "--commit", "--approve", "--review", review.Review.ID, "--message", "add download")
	if size := gitText(t, r.Path, "cat-file", "-s", committed.Review.Commit+":download.zip"); size != "69206016" {
		t.Fatalf("the committed blob must be the 66 MiB file, got %s bytes", size)
	}
	isolatedGit(t, r.Path, nil, "cherry-pick", committed.Review.Commit)
	laneReport(t, lane, nil, 0, "sync")
	laneReport(t, lane, nil, 0, "destroy")
}

func assertSameBytes(t *testing.T, a, b string) {
	t.Helper()
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA != nil || errB != nil || infoA.Size() != infoB.Size() {
		t.Fatalf("size differs or missing: %v %v", errA, errB)
	}
	fa, _ := os.Open(a)
	fb, _ := os.Open(b)
	defer fa.Close()
	defer fb.Close()
	bufA, bufB := make([]byte, 1<<20), make([]byte, 1<<20)
	for offset := int64(0); offset < infoA.Size(); {
		na, _ := fa.Read(bufA)
		nb, _ := fb.Read(bufB)
		if na != nb || string(bufA[:na]) != string(bufB[:nb]) {
			t.Fatalf("%s and %s differ near byte %d", a, b, offset)
		}
		if na == 0 {
			break
		}
		offset += int64(na)
	}
}

func TestOverTheHardLimitCanBeRaisedInTheSettingsFile(t *testing.T) {
	t.Parallel()
	r := testutil.NewRepository(t)
	r.Write("a.txt", "a\n")
	writeFilled(t, filepath.Join(r.Path, "huge.bin"), 70<<20, 5)
	r.Commit("one 70 MiB file")
	root := filepath.Join(r.Parent, "lanes")
	settingsFile := filepath.Join(r.Parent, "workcopy.env")
	if err := os.WriteFile(settingsFile, []byte("WORKCOPY_MAX_FILE_MIB=64\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := run(t, r.Path, nil, "create", "t", "--all", "--root", root, "--settings", settingsFile, "--json")
	errorCodeValue, message := errorOf(t, stdout)
	if code != 3 || errorCodeValue != "unsupported-source-entry" || !strings.Contains(message, "WORKCOPY_MAX_FILE_MIB") || !strings.Contains(message, "huge.bin") {
		t.Fatalf("a limit set in the settings file must apply and be named in the refusal: %d %s", code, stdout)
	}
	assertNothingCreated(t, r.Path, root)
	if err := os.WriteFile(settingsFile, []byte("WORKCOPY_MAX_FILE_MIB=128\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lane := laneReport(t, r.Path, []string{"WORKCOPY_SETTINGS=" + settingsFile}, 0, "create", "t", "--all", "--root", root).Workspace.Path
	assertSameBytes(t, filepath.Join(r.Path, "huge.bin"), filepath.Join(lane, "huge.bin"))
	laneReport(t, lane, nil, 0, "destroy")
}
