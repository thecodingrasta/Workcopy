package acceptance

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"workcopy/internal/testutil"
)

// A lane must survive its process dying at any instant. These tests start the
// shipped executable, kill it hard (TerminateProcess on Windows, SIGKILL
// elsewhere) after a spread of delays so it dies inside atomic writes, journal
// saves and manifest updates, then require the same thing every time: recover
// finishes the job, status is clean, and the lane matches what an uninterrupted
// run produces. The repository converts line endings, because that is the
// configuration whose recovery was broken.

func killAfter(t *testing.T, directory string, delay time.Duration, arguments ...string) (finished bool) {
	t.Helper()
	recordCommand(arguments...)
	command := exec.Command(executable, arguments...)
	command.Dir = directory
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(delay):
		_ = command.Process.Kill()
		<-done
		return false
	}
}

// killAtProgress starts the command and kills it once the lane holds the given
// share of its files. Timing a kill from the clock makes the result depend on
// machine load: a run under the full suite finished before most kills landed.
// Counting materialised files makes every kill land mid-flight by construction,
// because creation cannot finish before the lane holds all its files.
func killAtProgress(t *testing.T, directory, lane string, total int, fraction float64, arguments ...string) (finished bool) {
	t.Helper()
	recordCommand(arguments...)
	command := exec.Command(executable, arguments...)
	command.Dir = directory
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	target := int(float64(total) * fraction)
	deadline := time.Now().Add(3 * time.Minute)
	for {
		select {
		case err := <-done:
			return err == nil
		default:
		}
		if countLaneFiles(lane) >= target {
			_ = command.Process.Kill()
			<-done
			return false
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			<-done
			t.Fatalf("%s did not reach %d files within three minutes: %s", arguments[0], target, head1(stderr.String()))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// countLaneFiles counts materialised source files, ignoring Workcopy's own
// metadata and in-progress temporary files, and tolerates a directory that is
// being written while it is walked.
func countLaneFiles(lane string) int {
	count := 0
	_ = filepath.WalkDir(lane, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".workcopy" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(entry.Name(), ".workcopy-tmp-") {
			count++
		}
		return nil
	})
	return count
}

func convertingRepository(t *testing.T, files int) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write(".gitattributes", "* text eol=crlf\n*.bin -text\n")
	for n := 0; n < files; n++ {
		r.Write(fmt.Sprintf("src/g%02d/d%d/file%04d.txt", n%11, n%4, n), fmt.Sprintf("content %d\nline two\nline three\n", n))
	}
	r.Write("assets/raw.bin", "keep\r\nas is\r\n")
	r.Write("run.sh", "#!/bin/sh\necho ok\n")
	r.Commit("converting repository")
	return r
}

// snapshotLane returns every non-metadata file with its bytes, for comparison.
func snapshotLane(t *testing.T, lane string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(lane, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, _ := filepath.Rel(lane, path)
		relative = filepath.ToSlash(relative)
		if relative == ".workcopy" || strings.HasPrefix(relative, ".workcopy/") {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files[relative] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func sameFiles(t *testing.T, label string, got, want map[string]string) {
	t.Helper()
	missing, extra, differ := []string{}, []string{}, []string{}
	for name, body := range want {
		if actual, ok := got[name]; !ok {
			missing = append(missing, name)
		} else if actual != body {
			differ = append(differ, name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(differ)
	if len(missing)+len(extra)+len(differ) > 0 {
		t.Errorf("%s: lane differs from an uninterrupted run: missing %v, extra %v, different %v", label, head(missing), head(extra), head(differ))
	}
}

func head(names []string) []string {
	if len(names) > 5 {
		return append(names[:5:5], fmt.Sprintf("...(%d total)", len(names)))
	}
	return names
}

func recoverUntilReady(t *testing.T, directory, name, root, label string) {
	t.Helper()
	stdout, stderr, code := run(t, directory, nil, "recover", name, "--root", root, "--json")
	if code != 0 {
		t.Fatalf("%s: recover must finish the job, got exit %d: %s %s", label, code, head1(stdout), head1(stderr))
	}
}

func head1(text string) string {
	if len(text) > 400 {
		return text[:400] + "..."
	}
	return strings.TrimSpace(text)
}

func TestKilledCreateAlwaysRecoversToTheSameLane(t *testing.T) {
	t.Parallel()
	r := convertingRepository(t, 700)
	root := filepath.Join(r.Parent, "lanes")

	// The reference: an uninterrupted run whose lane is the exact target every
	// killed-and-recovered lane must match.
	reference := laneReport(t, r.Path, nil, 0, "create", "reference", "--all", "--root", root).Workspace.Path
	want := snapshotLane(t, reference)
	if want["src/g00/d0/file0000.txt"] != "content 0\r\nline two\r\nline three\r\n" {
		t.Fatalf("the reference lane is not CRLF: %q", want["src/g00/d0/file0000.txt"])
	}
	if want["assets/raw.bin"] != "keep\r\nas is\r\n" {
		t.Fatal("a -text file must be written as stored")
	}
	total := len(want)

	killed, completed := 0, 0
	for index, fraction := range []float64{0.05, 0.2, 0.4, 0.6, 0.75, 0.85, 0.92, 0.97} {
		name := fmt.Sprintf("kill%d", index)
		label := fmt.Sprintf("%s (killed at %d%% of %d files)", name, int(fraction*100), total)
		if killAtProgress(t, r.Path, filepath.Join(root, name), total, fraction, "create", name, "--all", "--root", root) {
			completed++
		} else {
			killed++
		}
		// The registry may not even hold the lane yet if the kill came very early.
		stdout, _, code := run(t, r.Path, nil, "status", name, "--root", root, "--json")
		if code == 3 && strings.Contains(stdout, "workspace-not-registered") {
			// Nothing was registered: the same name must simply be creatable.
			laneReport(t, r.Path, nil, 0, "create", name, "--all", "--root", root)
		} else if code != 0 {
			recoverUntilReady(t, r.Path, name, root, label)
		}
		lane := filepath.Join(root, name)
		sameFiles(t, label, snapshotLane(t, lane), want)
		report := laneReport(t, lane, nil, 0, "status")
		if len(report.Changes) != 0 {
			t.Errorf("%s: a recovered lane must be clean, status lists %v", label, head(changedPaths(report)))
		}
		laneReport(t, lane, nil, 0, "destroy")
	}
	if killed < 3 {
		t.Fatalf("only %d of 8 kills landed before creation finished (%d finished first), so this run proved too little", killed, completed)
	}
	t.Logf("killed %d creations mid-flight, %d finished before the kill", killed, completed)
}

func TestKilledSyncAndReviewRecoverWithConvertedFiles(t *testing.T) {
	t.Parallel()
	r := convertingRepository(t, 900)
	root := filepath.Join(r.Parent, "lanes")
	lane := laneReport(t, r.Path, nil, 0, "create", "work", "--all", "--root", root).Workspace.Path

	// Upstream changes many files, so sync has real work to be interrupted in.
	for n := 0; n < 900; n += 3 {
		r.Write(fmt.Sprintf("src/g%02d/d%d/file%04d.txt", n%11, n%4, n), fmt.Sprintf("content %d\nupstream edit\nline three\n", n))
	}
	r.Commit("upstream edits")
	killedSync := false
	for _, delay := range []time.Duration{15, 50, 120, 250} {
		if !killAfter(t, lane, delay*time.Millisecond, "sync") {
			killedSync = true
			break
		}
		// It finished before the kill; give the next attempt fresh upstream work.
		for n := 1; n < 900; n += 3 {
			r.Write(fmt.Sprintf("src/g%02d/d%d/file%04d.txt", n%11, n%4, n), fmt.Sprintf("content %d\nsecond upstream %d\nline three\n", n, delay))
		}
		r.Commit(fmt.Sprintf("more upstream %d", delay))
	}
	if !killedSync {
		t.Skip("sync finished before every kill on this machine; nothing was interrupted")
	}
	if code := mustExit(t, lane, "status"); code != 0 {
		recoverUntilReady(t, lane, "work", root, "after killed sync")
	}
	synced := laneReport(t, lane, nil, 0, "status")
	if len(synced.Changes) != 0 || len(synced.Conflicts) != 0 {
		t.Fatalf("a recovered sync must leave a clean lane (killed=%v): changes %v conflicts %v", killedSync, head(changedPaths(synced)), synced.Conflicts)
	}
	laneReport(t, lane, nil, 0, "sync") // idempotent and a no-op after recovery, or exit 4 if already current
}

func mustExit(t *testing.T, directory string, arguments ...string) int {
	t.Helper()
	_, _, code := run(t, directory, nil, append(arguments, "--json")...)
	return code
}

func TestKilledDestroyIsFinishedByRecover(t *testing.T) {
	t.Parallel()
	r := convertingRepository(t, 900)
	root := filepath.Join(r.Parent, "lanes")
	lane := laneReport(t, r.Path, nil, 0, "create", "doomed", "--all", "--root", root).Workspace.Path
	killed := false
	for _, delay := range []time.Duration{20, 70, 150, 300} {
		if !killAfter(t, r.Path, delay*time.Millisecond, "destroy", "doomed", "--root", root) {
			killed = true
			break
		}
		lane = laneReport(t, r.Path, nil, 0, "create", "doomed", "--all", "--root", root).Workspace.Path
	}
	if !killed {
		t.Skip("destroy always finished before the kill on this machine")
	}
	listed := laneReport(t, r.Path, nil, 0, "list", "--root", root)
	for _, item := range listed.Workspaces {
		if item.Name == "doomed" && item.Phase != "ready" {
			recoverUntilReady(t, r.Path, "doomed", root, "interrupted destroy")
		}
	}
	// Whether the kill came before or after the point of no return, the owner
	// can always reach "no lane" with the ordinary commands.
	if _, _, code := run(t, r.Path, nil, "destroy", "doomed", "--root", root, "--json"); code != 0 && code != 3 {
		t.Fatalf("destroy after a killed destroy: exit %d", code)
	}
	if _, err := os.Lstat(lane); err == nil {
		laneReport(t, r.Path, nil, 0, "destroy", "doomed", "--root", root, "--force")
	}
	if after := laneReport(t, r.Path, nil, 0, "list", "--root", root); len(after.Workspaces) != 0 {
		t.Fatalf("the lane must be gone: %+v", after.Workspaces)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(root, ".workcopy-trash-*")); len(leftovers) != 0 {
		t.Fatalf("quarantine directories must not be left behind: %v", leftovers)
	}
	if pins := gitText(t, r.Path, "for-each-ref", "refs/workcopy"); pins != "" {
		t.Fatalf("pinned refs must be removed:\n%s", pins)
	}
}
