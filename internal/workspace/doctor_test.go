package workspace

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"workcopy/internal/git"
	"workcopy/internal/testutil"
)

// WC-002 / WC-006: the warning fires for the combination that goes wrong (the
// Linux build under WSL on a Windows drive) and for nothing else.
func TestDriveWarningsFireOnlyForLinuxBuildOnAWindowsDriveUnderWSL(t *testing.T) {
	locations := func(paths ...string) []storageLocation {
		result := []storageLocation{}
		for n, path := range paths {
			result = append(result, storageLocation{[]string{"repository", "lane storage"}[n], path})
		}
		return result
	}
	cases := []struct {
		name  string
		goos  string
		wsl   bool
		paths []string
		want  int
	}{
		{"repo on drive", "linux", true, []string{"/mnt/c/Users/me/repo", "/home/me/lanes"}, 1},
		{"both on drive", "linux", true, []string{"/mnt/c/repo", "/mnt/d/lanes"}, 2},
		{"drive root", "linux", true, []string{"/mnt/c", "/home/x"}, 1},
		{"native ext4", "linux", true, []string{"/home/me/repo", "/home/me/lanes"}, 0},
		{"not WSL", "linux", false, []string{"/mnt/c/repo", "/mnt/c/lanes"}, 0},
		{"similar prefix", "linux", true, []string{"/mnt/cc/repo", "/mnt/wsl/lanes"}, 0},
		{"windows build", "windows", false, []string{"/mnt/c/repo"}, 0},
		{"macOS", "darwin", false, []string{"/mnt/c/repo"}, 0},
	}
	for _, c := range cases {
		got := driveWarnings(c.goos, c.wsl, locations(c.paths...))
		if len(got) != c.want {
			t.Errorf("%s: %d warnings, want %d: %v", c.name, len(got), c.want, got)
		}
		for _, warning := range got {
			if !strings.Contains(warning, "drvfs/9p") {
				t.Errorf("%s: warning does not mention drvfs/9p: %s", c.name, warning)
			}
			// Each warning carries the remedy for the location it is about.
			switch {
			case strings.Contains(warning, "lane storage"):
				for _, must := range []string{"WORKCOPY_ROOT", "WSL filesystem", "repository can stay"} {
					if !strings.Contains(warning, must) {
						t.Errorf("%s: lane storage warning lacks %q: %s", c.name, must, warning)
					}
				}
			default:
				for _, must := range []string{"prunable", "workcopy.exe"} {
					if !strings.Contains(warning, must) {
						t.Errorf("%s: repository warning lacks %q: %s", c.name, must, warning)
					}
				}
			}
		}
	}
}

// Creating a lane on a Windows drive from WSL says so in the create report itself,
// where an agent or person will read it, and does so only there.
func TestCreateWarnsAboutLaneStorageOnAWindowsDrive(t *testing.T) {
	if got := LaneStorageWarning("/home/me/lanes"); got != "" {
		t.Fatalf("ext4 lane storage must not warn: %s", got)
	}
	was := underWSL
	t.Cleanup(func() { underWSL = was })
	underWSL = func() bool { return true }
	want := runtime.GOOS == "linux"
	if got := LaneStorageWarning("/mnt/c/lanes"); (got != "") != want || want && !strings.Contains(got, "WORKCOPY_ROOT") {
		t.Fatalf("on %s a /mnt/c lane root should warn=%v, got %q", runtime.GOOS, want, got)
	}
}

func TestDoctorReportsGitProgramLineEndingsAndRepository(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("a", "x")
	r.Commit("base")
	r.Git("config", "core.autocrlf", "true")
	report, err := Doctor(context.Background(), "test-version", r.Path, "")
	if err != nil {
		t.Fatal(err)
	}
	gitPath, _ := exec.LookPath("git")
	for _, want := range []string{"Workcopy test-version", "Git: git version", gitPath, "Repository: ", "core.autocrlf=true", "No installations"} {
		if !strings.Contains(report.Message, want) {
			t.Errorf("doctor message lacks %q:\n%s", want, report.Message)
		}
	}
	// It must not have created anything beside the repository.
	if report.ExitCode != 0 || len(report.Warnings) < 2 {
		t.Fatalf("baseline warnings missing: %+v", report)
	}

	t.Setenv(git.ExecutableEnvironment, gitPath)
	report, err = Doctor(context.Background(), "test-version", r.Path, "")
	if err != nil || !strings.Contains(report.Message, git.ExecutableEnvironment+"="+gitPath) {
		t.Fatalf("an override in effect must be reported: %v\n%s", err, report.Message)
	}

	outside := t.TempDir()
	report, err = Doctor(context.Background(), "test-version", outside, "")
	if err != nil || !strings.Contains(report.Message, "No Git repository found") {
		t.Fatalf("doctor outside a repository should still work: %v\n%s", err, report.Message)
	}
}
