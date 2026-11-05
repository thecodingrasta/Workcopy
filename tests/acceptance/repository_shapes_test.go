package acceptance

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

// These tests drive the shipped executable against the repository and
// filesystem shapes real users have: shallow clones, linked worktrees, sparse
// checkouts, odd names, deep paths, binary content, submodules, bare and empty
// repositories. They share one lifecycle so a single run proves several
// properties at once: the lane mirrors the pinned commit byte for byte, Workcopy
// never touches the main checkout, the reviewed commit contains exactly the
// lane's changes, and nothing is left behind afterwards.

// isolatedGit runs Git with a fixed identity and no user or system config and
// returns stdout exactly as written (no trimming, so blobs stay byte-exact).
func isolatedGit(t *testing.T, directory string, stdin []byte, arguments ...string) []byte {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(value), "GIT_") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Fixture Author", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Fixture Author", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", arguments, directory, err, stderr.String())
	}
	return stdout.Bytes()
}

func gitText(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	return strings.TrimSpace(string(isolatedGit(t, directory, nil, arguments...)))
}

// blobID is Git's object ID for content, so lane bytes can be compared with the
// committed tree without a Git process per file.
func blobID(t *testing.T, directory string, content []byte) string {
	t.Helper()
	header := fmt.Sprintf("blob %d\x00", len(content))
	format, cached := objectFormats.Load(directory)
	if !cached {
		format, _ = objectFormats.LoadOrStore(directory, gitText(t, directory, "rev-parse", "--show-object-format"))
	}
	if format == "sha256" {
		sum := sha256.Sum256(append([]byte(header), content...))
		return hex.EncodeToString(sum[:])
	}
	sum := sha1.Sum(append([]byte(header), content...))
	return hex.EncodeToString(sum[:])
}

// objectFormats caches each repository's hash algorithm so hashing thousands
// of lane files does not start a Git process per file.
var objectFormats sync.Map

type treeRecord struct{ mode, kind, id, path string }

func treeRecords(t *testing.T, directory, commit string) []treeRecord {
	t.Helper()
	var records []treeRecord
	for _, record := range bytes.Split(isolatedGit(t, directory, nil, "ls-tree", "-r", "-z", commit), []byte{0}) {
		if len(record) == 0 {
			continue
		}
		header, name, ok := strings.Cut(string(record), "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 3 {
			t.Fatalf("unexpected ls-tree record %q", record)
		}
		records = append(records, treeRecord{fields[0], fields[1], fields[2], name})
	}
	return records
}

// honestExecutableBits reports whether the filesystem under directory stores
// the owner execute bit faithfully (not true on FAT or no-metadata drvfs).
func honestExecutableBits(directory string) bool {
	plain, executable := filepath.Join(directory, ".wc-mode-plain"), filepath.Join(directory, ".wc-mode-exec")
	defer os.Remove(plain)
	defer os.Remove(executable)
	if os.WriteFile(plain, nil, 0644) != nil || os.WriteFile(executable, nil, 0755) != nil {
		return false
	}
	_ = os.Chmod(plain, 0644)
	_ = os.Chmod(executable, 0755)
	a, errA := os.Stat(plain)
	b, errB := os.Stat(executable)
	return errA == nil && errB == nil && a.Mode().Perm()&0111 == 0 && b.Mode().Perm()&0111 != 0
}

// assertLaneMirrorsCommit checks that the lane holds exactly the commit's
// files and symbolic links (and nothing else but Workcopy's own metadata), with
// identical bytes, and that Git-only entries were not materialized.
func assertLaneMirrorsCommit(t *testing.T, repository, commit, lane string) {
	t.Helper()
	expected := map[string]treeRecord{}
	for _, record := range treeRecords(t, repository, commit) {
		if record.kind == "blob" {
			expected[record.path] = record
		}
	}
	checkModes := runtime.GOOS != "windows" && honestExecutableBits(filepath.Dir(lane))
	seen := map[string]bool{}
	err := filepath.WalkDir(lane, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, relErr := filepath.Rel(lane, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if relative == ".git" || strings.HasPrefix(relative, ".git/") {
			t.Errorf("lane contains Git metadata: %s", relative)
		}
		if relative == ".workcopy" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		want, ok := expected[relative]
		if !ok {
			t.Errorf("lane has %s, which is not in the commit", relative)
			return nil
		}
		seen[relative] = true
		if entry.Type()&os.ModeSymlink != 0 {
			target, linkErr := os.Readlink(path)
			if linkErr != nil || want.mode != "120000" || blobID(t, repository, []byte(filepath.ToSlash(target))) != want.id {
				t.Errorf("symbolic link %s: target %q (%v) does not match mode %s object %s", relative, target, linkErr, want.mode, want.id)
			}
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Errorf("read %s: %v", relative, readErr)
			return nil
		}
		if blobID(t, repository, body) != want.id {
			t.Errorf("%s differs from the commit (%d bytes in lane)", relative, len(body))
		}
		if checkModes {
			info, _ := entry.Info()
			if executable := info != nil && info.Mode().Perm()&0100 != 0; executable != (want.mode == "100755") {
				t.Errorf("%s execute bit is %v, commit mode is %s", relative, executable, want.mode)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name := range expected {
		if !seen[name] {
			t.Errorf("lane is missing %s", name)
		}
	}
}

type shapeCase struct {
	repo *testutil.Repository
	// dir is where the binary starts; top is the work tree root (they differ when
	// the user is in a subdirectory or a linked worktree).
	dir, top string
	// source is the repository whose HEAD the lane must mirror; empty means repo.Path.
	source      string
	env         []string
	edit, drop  string
	integrate   bool
	defaultRoot bool
}

func laneReport(t *testing.T, directory string, environment []string, expected int, arguments ...string) workspace.Report {
	t.Helper()
	stdout, stderr, code := run(t, directory, environment, append(arguments, "--json")...)
	var report workspace.Report
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("%v: invalid JSON (%v): %q stderr=%q", arguments, err, stdout, stderr)
	}
	if code != expected || report.ExitCode != code {
		t.Fatalf("%v: want exit %d, process %d, JSON %d: %s", arguments, expected, code, report.ExitCode, stdout)
	}
	return report
}

func changedPaths(report workspace.Report) []string {
	paths := make([]string, 0, len(report.Changes))
	for _, change := range report.Changes {
		paths = append(paths, change.Path)
	}
	sort.Strings(paths)
	return paths
}

// exerciseShape is the shared lifecycle: create, mutate, review, commit,
// integrate, sync and destroy, asserting the invariants at every step.
func exerciseShape(t *testing.T, c shapeCase) {
	t.Helper()
	source := c.source
	if source == "" {
		source = c.repo.Path
	}
	commit := gitText(t, source, "rev-parse", "HEAD")
	human := filepath.Join(c.top, "human-notes.txt")
	if err := os.WriteFile(human, []byte("untracked human work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mainBefore := isolatedGit(t, source, nil, "status", "--porcelain=v1", "-z")
	root := filepath.Join(c.repo.Parent, "lanes with spaces")
	rootArgs := []string{"--root", root}
	if c.defaultRoot {
		rootArgs = nil
	}

	created := laneReport(t, c.dir, c.env, 0, append([]string{"create", "l1", "--all"}, rootArgs...)...)
	lane := created.Workspace.Path
	if _, err := os.Lstat(filepath.Join(lane, ".git")); !os.IsNotExist(err) {
		t.Fatalf("lane must be Git-free: %v", err)
	}
	assertLaneMirrorsCommit(t, source, commit, lane)
	if doctor := laneReport(t, c.dir, c.env, 0, "doctor"); !strings.Contains(doctor.Message, "Repository:") {
		t.Fatalf("doctor must describe the repository: %s", doctor.Message)
	}
	if clean := laneReport(t, lane, c.env, 0, "status"); len(clean.Changes) != 0 {
		t.Fatalf("a fresh lane must have no changes: %v", changedPaths(clean))
	}

	// The lane edits one file, adds one with an awkward name and deletes one.
	added := "added by lane/ünï new file.txt"
	editPath, dropPath := filepath.Join(lane, filepath.FromSlash(c.edit)), filepath.Join(lane, filepath.FromSlash(c.drop))
	original, err := os.ReadFile(editPath)
	if err != nil {
		t.Fatal(err)
	}
	appended := append(append([]byte{}, original...), []byte("\nedited by lane\n")...)
	if err = os.WriteFile(editPath, appended, 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(lane, "added by lane"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(lane, filepath.FromSlash(added)), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(dropPath); err != nil {
		t.Fatal(err)
	}
	wantChanges := []string{added, c.drop, c.edit}
	sort.Strings(wantChanges)
	if got := changedPaths(laneReport(t, lane, c.env, 0, "status")); strings.Join(got, "\x00") != strings.Join(wantChanges, "\x00") {
		t.Fatalf("status must report exactly the three edits:\n got %q\nwant %q", got, wantChanges)
	}

	review := laneReport(t, lane, c.env, 0, "merge", "--include-files")
	if review.Review == nil {
		t.Fatal("merge produced no review")
	}
	if body, readErr := os.ReadFile(filepath.Join(review.Review.Path, filepath.FromSlash(added))); readErr != nil || string(body) != "new\n" {
		t.Fatalf("the review is missing the added file: %v", readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(review.Review.Path, filepath.FromSlash(c.drop))); !os.IsNotExist(statErr) {
		t.Fatalf("the review must not contain the deleted file: %v", statErr)
	}
	committed := laneReport(t, lane, c.env, 0, "merge", "--commit", "--approve", "--review", review.Review.ID, "--message", "Reviewed contribution")
	reviewCommit := committed.Review.Commit

	// The commit is the lane's three changes on top of the pinned commit, no more.
	if parent := gitText(t, source, "rev-parse", reviewCommit+"^"); parent != commit {
		t.Fatalf("review parent %s is not the pinned commit %s", parent, commit)
	}
	raw := bytes.Split(isolatedGit(t, source, nil, "diff-tree", "-r", "-z", "--no-renames", "--raw", "--no-commit-id", reviewCommit), []byte{0})
	got := []string{}
	for index := 0; index+1 < len(raw); index += 2 {
		status := string(raw[index])
		got = append(got, status[len(status)-1:]+" "+string(raw[index+1]))
	}
	sort.Strings(got)
	want := []string{"A " + added, "D " + c.drop, "M " + c.edit}
	sort.Strings(want)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("committed changes differ:\n got %q\nwant %q", got, want)
	}
	if body := isolatedGit(t, source, nil, "show", reviewCommit+":"+c.edit); !bytes.Equal(body, appended) {
		t.Fatalf("committed %s does not hold the lane's bytes", c.edit)
	}

	// Main is exactly as the human left it.
	if gitText(t, source, "rev-parse", "HEAD") != commit || !bytes.Equal(isolatedGit(t, source, nil, "status", "--porcelain=v1", "-z"), mainBefore) {
		t.Fatal("Workcopy changed the main checkout")
	}
	if body, readErr := os.ReadFile(human); readErr != nil || string(body) != "untracked human work\n" {
		t.Fatalf("human work was disturbed: %v", readErr)
	}

	if c.integrate {
		if _, _, code := run(t, lane, c.env, "destroy", "--json"); code != 3 {
			t.Fatalf("destroy must refuse a lane with unintegrated work, got exit %d", code)
		}
		isolatedGit(t, c.top, nil, "cherry-pick", reviewCommit)
		laneReport(t, lane, c.env, 0, "sync")
		laneReport(t, lane, c.env, 0, "destroy")
	} else {
		laneReport(t, lane, c.env, 3, "destroy")
		laneReport(t, lane, c.env, 0, "destroy", "--force")
	}
	if _, err = os.Lstat(lane); !os.IsNotExist(err) {
		t.Fatalf("destroy left the lane behind: %v", err)
	}
	listed := laneReport(t, c.dir, c.env, 0, append([]string{"list"}, rootArgs...)...)
	if len(listed.Workspaces) != 0 {
		t.Fatalf("a destroyed lane is still registered: %+v", listed.Workspaces)
	}
	if pins := gitText(t, source, "for-each-ref", "refs/workcopy"); pins != "" {
		t.Fatalf("destroy left pinned refs behind:\n%s", pins)
	}
}

func newRepositoryAt(t *testing.T, name string) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	if name == "repository" {
		return r
	}
	custom := filepath.Join(r.Parent, name)
	if err := os.Rename(r.Path, custom); err != nil {
		t.Fatal(err)
	}
	r.Path = custom
	return r
}

func plainFiles(r *testutil.Repository) {
	r.Write("README.md", "# fixture\n")
	r.Write("src/app.txt", "application\n")
	r.Write("src/drop.txt", "to be deleted\n")
	r.Write("docs/guide.md", "guide\n")
}

func shapeFor(r *testutil.Repository) shapeCase {
	return shapeCase{repo: r, dir: r.Path, top: r.Path, edit: "src/app.txt", drop: "src/drop.txt", integrate: true}
}

// xorshiftBytes is deterministic incompressible content for the large-blob case.
func xorshiftBytes(count int) []byte {
	state, out := uint64(0x9E3779B97F4A7C15), make([]byte, count)
	for index := range out {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		out[index] = byte(state)
	}
	return out
}

func symlinksAvailable(t *testing.T) bool {
	t.Helper()
	directory := t.TempDir()
	if err := os.Symlink("target", filepath.Join(directory, "probe")); err != nil {
		return false
	}
	return true
}

func TestRepositoryShapesRoundTripThroughTheRealBinary(t *testing.T) {
	shapes := map[string]func(t *testing.T) shapeCase{
		"unicode spaces and punctuation in names": func(t *testing.T) shapeCase {
			r := testutil.NewRepository(t)
			for _, name := range []string{"README.md", "src/日本語/ファイル.txt", "src/ünï cödé/é.txt", "docs/dir with spaces/a b.txt", "src/semi;colon&amp #hash %percent.txt", "src/-leading-dash.txt", "src/quote'single.txt", "src/emoji-😀.txt", "src/app.txt", "src/drop.txt"} {
				r.Write(name, "content of "+name+"\n")
			}
			r.Commit("unicode names")
			return shapeFor(r)
		},
		"empty binary large and unusual line-ending content": func(t *testing.T) shapeCase {
			r := testutil.NewRepository(t)
			everyByte := make([]byte, 256)
			for index := range everyByte {
				everyByte[index] = byte(index)
			}
			r.Write("empty.txt", "")
			r.Write("every-byte.bin", string(everyByte))
			r.Write("large.bin", string(xorshiftBytes(3<<20)))
			r.Write("no-newline.txt", "abc")
			r.Write("bom.txt", "\xEF\xBB\xBFhello\r\n")
			r.Write("crlf.txt", "a\r\nb\r\n")
			r.Write("lone-cr.txt", "a\rb")
			r.Write("mixed.txt", "a\r\nb\nc\r")
			r.Commit("unusual content")
			c := shapeFor(r)
			c.edit, c.drop = "crlf.txt", "empty.txt"
			return c
		},
		"detached HEAD": func(t *testing.T) shapeCase {
			r := testutil.NewRepository(t)
			plainFiles(r)
			r.Commit("first")
			r.Write("src/app.txt", "second\n")
			r.Commit("second")
			r.Git("checkout", "--detach")
			c := shapeFor(r)
			c.integrate = false // a detached lane is pinned to its commit and does not follow later commits
			return c
		},
		"shallow clone": func(t *testing.T) shapeCase {
			origin := testutil.NewRepository(t)
			plainFiles(origin)
			for _, revision := range []string{"1", "2", "3"} {
				origin.Write("src/app.txt", revision+"\n")
				origin.Commit("revision " + revision)
			}
			clone := filepath.Join(origin.Parent, "shallow clone")
			url := "file://" + filepath.ToSlash(origin.Path)
			if !strings.HasPrefix(filepath.ToSlash(origin.Path), "/") {
				url = "file:///" + filepath.ToSlash(origin.Path)
			}
			isolatedGit(t, origin.Parent, nil, "clone", "--depth", "1", url, clone)
			if gitText(t, clone, "rev-parse", "--is-shallow-repository") != "true" {
				t.Fatal("fixture is not shallow")
			}
			shallow := &testutil.Repository{T: t, Parent: origin.Parent, Path: clone}
			shallow.Git("config", "user.name", "Fixture Author")
			shallow.Git("config", "user.email", "fixture@example.invalid")
			return shapeFor(shallow)
		},
		"linked worktree": func(t *testing.T) shapeCase {
			r := testutil.NewRepository(t)
			plainFiles(r)
			r.Commit("base")
			linked := filepath.Join(r.Parent, "linked worktree")
			r.Git("worktree", "add", linked, "-b", "feature")
			c := shapeFor(r)
			c.dir, c.top = linked, linked
			return c
		},
		"sparse checkout": func(t *testing.T) shapeCase {
			r := testutil.NewRepository(t)
			plainFiles(r)
			r.Commit("base")
			r.Git("sparse-checkout", "set", "src")
			c := shapeFor(r)
			c.integrate = false // Workcopy reads the commit, so the cone is irrelevant to the lane
			return c
		},
		"started from a subdirectory": func(t *testing.T) shapeCase {
			r := testutil.NewRepository(t)
			plainFiles(r)
			r.Commit("base")
			c := shapeFor(r)
			c.dir = filepath.Join(r.Path, "src")
			return c
		},
		"ambient GIT_DIR pointing at another repository": func(t *testing.T) shapeCase {
			r := testutil.NewRepository(t)
			plainFiles(r)
			r.Commit("base")
			other := testutil.NewRepository(t)
			other.Write("other.txt", "someone else's repository\n")
			other.Commit("other")
			c := shapeFor(r)
			c.env = []string{"GIT_DIR=" + filepath.Join(other.Path, ".git"), "GIT_WORK_TREE=" + other.Path, "GIT_INDEX_FILE=" + filepath.Join(other.Path, ".git", "index")}
			return c
		},
		"repository path with spaces and Unicode using the default lane root": func(t *testing.T) shapeCase {
			r := newRepositoryAt(t, "répo with spaces 日本語")
			plainFiles(r)
			r.Commit("base")
			c := shapeFor(r)
			c.defaultRoot = true
			return c
		},
		"1200 files in nested directories": func(t *testing.T) shapeCase {
			r := testutil.NewRepository(t)
			for index := 0; index < 1200; index++ {
				r.Write(fmt.Sprintf("bulk/group%02d/file%04d.txt", index%50, index), fmt.Sprintf("file %d\n", index))
			}
			r.Commit("bulk")
			c := shapeFor(r)
			c.edit, c.drop = "bulk/group01/file0001.txt", "bulk/group02/file0002.txt"
			return c
		},
		"path longer than Windows MAX_PATH": func(t *testing.T) shapeCase {
			r := testutil.NewRepository(t)
			r.Git("config", "core.longpaths", "true")
			deep := strings.Repeat("d", 50)
			long := "deep/" + strings.Join([]string{deep + "1", deep + "2", deep + "3", deep + "4", deep + "5"}, "/")
			r.Write(long+"/target.txt", "deep\n")
			r.Write(long+"/drop.txt", "deep drop\n")
			r.Write("README.md", "root\n")
			r.Commit("deep")
			c := shapeFor(r)
			c.edit, c.drop = long+"/target.txt", long+"/drop.txt"
			return c
		},
	}
	if symlinksAvailable(t) {
		shapes["executable files and relative symbolic links"] = func(t *testing.T) shapeCase {
			r := testutil.NewRepository(t)
			commitRawEntries(t, r, []rawEntry{
				{"100755", "bin/run.sh", "#!/bin/sh\necho ok\n"},
				{"100644", "src/app.txt", "application\n"},
				{"100644", "src/drop.txt", "to be deleted\n"},
				{"120000", "link-to-script", "bin/run.sh"},
				{"120000", "src/link-up", "../bin/run.sh"},
			}, true)
			return shapeFor(r)
		}
	}
	names := make([]string, 0, len(shapes))
	for name := range shapes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		build := shapes[name]
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			exerciseShape(t, build(t))
		})
	}
}

// rawEntry is a tree entry written straight into Git's object store, so
// fixtures can hold paths and links that a work tree on this OS cannot.
type rawEntry struct{ mode, path, content string }

// commitRawEntries replaces main's content with a tree built from entries.
// With checkout it also resets the work tree so later Git commands see a clean
// repository (only safe for paths this OS can represent).
func commitRawEntries(t *testing.T, r *testutil.Repository, entries []rawEntry, checkout bool) string {
	t.Helper()
	tree := buildTree(t, r.Path, entries)
	arguments := []string{"commit-tree", tree, "-m", "raw entries"}
	if head, err := exec.Command("git", "-C", r.Path, "rev-parse", "--verify", "-q", "HEAD").Output(); err == nil {
		arguments = append(arguments, "-p", strings.TrimSpace(string(head)))
	}
	commit := gitText(t, r.Path, arguments...)
	isolatedGit(t, r.Path, nil, "update-ref", "refs/heads/main", commit)
	if checkout {
		isolatedGit(t, r.Path, nil, "reset", "--hard", "-q")
	}
	return commit
}

func buildTree(t *testing.T, repository string, entries []rawEntry) string {
	t.Helper()
	groups := map[string][]rawEntry{}
	var listing bytes.Buffer
	order := []string{}
	for _, entry := range entries {
		head, rest, nested := strings.Cut(entry.path, "/")
		if !nested {
			blob := strings.TrimSpace(string(isolatedGit(t, repository, []byte(entry.content), "hash-object", "-w", "--stdin")))
			fmt.Fprintf(&listing, "%s blob %s\t%s\x00", entry.mode, blob, head)
			continue
		}
		if _, seen := groups[head]; !seen {
			order = append(order, head)
		}
		groups[head] = append(groups[head], rawEntry{entry.mode, rest, entry.content})
	}
	for _, head := range order {
		fmt.Fprintf(&listing, "040000 tree %s\t%s\x00", buildTree(t, repository, groups[head]), head)
	}
	return strings.TrimSpace(string(isolatedGit(t, repository, listing.Bytes(), "mktree", "-z")))
}
