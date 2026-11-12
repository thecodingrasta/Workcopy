package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"workcopy/internal/testutil"
)

// WC-016: a review worktree sits deep inside the lane root, so files that are
// short in the repository can exceed Windows' 260-character limit there. Git for
// Windows then reports them as modified ("Filename too long") in a review where
// nothing was touched. Workcopy cannot change a user's Git configuration, so it
// must say so, with the command that fixes it, and must not nag once fixed.

func userGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, _ := command.CombinedOutput()
	return strings.TrimSpace(string(out))
}

func deepRepository(t *testing.T) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	deep := strings.Repeat("d", 40)
	long := "ui/" + strings.Join([]string{deep + "1", deep + "2", deep + "3", deep + "4"}, "/")
	commitRawEntries(t, r, []rawEntry{
		{"100644", "README.md", "root\n"},
		{"100644", long + "/image-one.png", "png one\n"},
		{"100644", long + "/image-two.png", "png two\n"},
	}, false)
	return r
}

func TestReviewWarnsWhenGitForWindowsCannotReadItsPaths(t *testing.T) {
	t.Parallel()
	r := deepRepository(t)
	root := filepath.Join(r.Parent, "lanes with a reasonably long name", "and-another-level")
	lane := laneReport(t, r.Path, nil, 0, "create", "reviewlane", "--all", "--root", root).Workspace.Path
	if err := os.WriteFile(filepath.Join(lane, "README.md"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	review := laneReport(t, lane, nil, 0, "merge")
	longest := 0
	_ = filepath.WalkDir(review.Review.Path, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && len(p) > longest {
			longest = len(p)
		}
		return nil
	})
	warned := strings.Contains(strings.Join(review.Warnings, "\n"), "core.longpaths")

	if runtime.GOOS != "windows" {
		if warned {
			t.Fatalf("the limit does not exist off Windows, so there must be no warning: %v", review.Warnings)
		}
		return
	}
	if longest < 260 {
		t.Fatalf("the fixture must exceed MAX_PATH to mean anything, longest is %d", longest)
	}
	if !warned {
		t.Fatalf("a review with a %d-character path must warn (core.longpaths unset): %v", longest, review.Warnings)
	}
	advice := ""
	for _, w := range review.Warnings {
		if strings.Contains(w, "core.longpaths") {
			advice = w
		}
	}
	for _, must := range []string{"Filename too long", "config core.longpaths true", r.Path} {
		if !strings.Contains(advice, must) {
			t.Errorf("the warning must contain %q: %s", must, advice)
		}
	}

	// The symptom the warning predicts is real, and the advertised fix removes it.
	before := userGit(t, review.Review.Path, "status", "--porcelain")
	if !strings.Contains(before, "Filename too long") {
		t.Fatalf("the user's git status was expected to report the long paths, got:\n%s", before)
	}
	userGit(t, r.Path, "config", "core.longpaths", "true")
	after := userGit(t, review.Review.Path, "status", "--porcelain")
	if strings.Contains(after, "Filename too long") || strings.TrimSpace(after) != "M README.md" {
		t.Fatalf("after the advertised fix git status must show only the real change, got:\n%s", after)
	}
	// And Workcopy stops warning once the setting is on.
	laneReport(t, lane, nil, 0, "destroy", "--force")
	second := laneReport(t, r.Path, nil, 0, "create", "second", "--all", "--root", root).Workspace.Path
	if err := os.WriteFile(filepath.Join(second, "README.md"), []byte("changed again\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if next := laneReport(t, second, nil, 0, "merge"); strings.Contains(strings.Join(next.Warnings, "\n"), "core.longpaths") {
		t.Fatalf("no warning once core.longpaths is on: %v", next.Warnings)
	}
}

func TestDoctorEstimatesLongReviewPathsBeforeTheFirstReview(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("the Windows path limit does not exist on this platform")
	}
	r := deepRepository(t)
	root := filepath.Join(r.Parent, "lanes with a reasonably long name", "and-another-level", "and-one-more")
	out := laneReport(t, r.Path, nil, 0, "doctor", "--root", root)
	if !strings.Contains(strings.Join(out.Warnings, "\n"), "Estimate for a typical review") {
		t.Fatalf("doctor must warn ahead of time: %v", out.Warnings)
	}
	userGit(t, r.Path, "config", "core.longpaths", "true")
	out = laneReport(t, r.Path, nil, 0, "doctor", "--root", root)
	if strings.Contains(strings.Join(out.Warnings, "\n"), "core.longpaths") {
		t.Fatalf("doctor must be quiet once it is on: %v", out.Warnings)
	}
}
