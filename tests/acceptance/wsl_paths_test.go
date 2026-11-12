package acceptance

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"workcopy/internal/fsops"
)

// WC-022: the Windows build is driven from WSL with the /mnt/<drive> paths the
// agent already has, and answers in kind; no wslpath wrapper is needed.
func TestWindowsBuildTakesAndAnswersInWSLSpelling(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Windows build translates /mnt/<drive>; the Linux build's direction is covered by tests/interop under WSL")
	}
	t.Parallel()
	r := projectFixture(t, "wsl")
	wslRepo := fsops.Render(r.Path, fsops.FormWSL)
	wslRoot := fsops.Render(filepath.Join(r.Parent, "lanes"), fsops.FormWSL)
	if !strings.HasPrefix(wslRepo, "/mnt/") {
		t.Skipf("the test repository %s is not on a drive letter", r.Path)
	}

	created := laneReport(t, r.Parent, nil, 0, "create", "task", "--all", "--repo", wslRepo, "--root", wslRoot)
	if created.Workspace.Path != wslRoot+"/task" {
		t.Fatalf("the lane must be reported in the form it was requested: %q, want %q", created.Workspace.Path, wslRoot+"/task")
	}
	native := filepath.Join(r.Parent, "lanes", "task")
	if body, err := os.ReadFile(filepath.Join(native, "app", "main.txt")); err != nil || !strings.Contains(string(body), "main") {
		t.Fatalf("the lane exists at the native path: %v", err)
	}

	// Human output and the bare path command speak the same way.
	if out, _, code := run(t, r.Parent, nil, "path", "task", "--repo", wslRepo, "--root", wslRoot); code != 0 || strings.TrimSpace(out) != wslRoot+"/task" {
		t.Errorf("path: %d %q", code, out)
	}
	if out, _, code := run(t, r.Parent, nil, "list", "--repo", wslRepo, "--root", wslRoot); code != 0 || !strings.Contains(out, wslRoot+"/task") || strings.Contains(out, `\`) {
		t.Errorf("list in human form: %d %q", code, out)
	}
	// Errors and suggestions are rewritten too: a person told to run a command
	// with C:\ paths when they gave /mnt/c ones would have to translate it back.
	out, _, code := run(t, r.Parent, nil, "destroy", "missing", "--repo", wslRepo, "--root", wslRoot, "--json")
	if code != 3 || strings.Contains(out, `C:\\`) {
		t.Errorf("an error for a /mnt caller must not mention C:\\ paths: %d %s", code, out)
	}

	// A caller using native paths on the same machine still gets native paths.
	if got := laneReport(t, r.Parent, nil, 0, "path", "task", "--repo", r.Path, "--root", filepath.Join(r.Parent, "lanes")).Workspace.Path; got != native {
		t.Errorf("native in, native out: %q", got)
	}
	// The style can be forced either way, whatever was passed.
	if got := laneReport(t, r.Parent, nil, 0, "path", "task", "--repo", r.Path, "--root", filepath.Join(r.Parent, "lanes"), "--path-style", "wsl").Workspace.Path; got != wslRoot+"/task" {
		t.Errorf("--path-style wsl: %q", got)
	}
	if got := laneReport(t, r.Parent, nil, 0, "path", "task", "--repo", wslRepo, "--root", wslRoot, "--path-style", "windows").Workspace.Path; got != native {
		t.Errorf("--path-style windows: %q", got)
	}
	env := []string{"WORKCOPY_PATH_STYLE=wsl"}
	if got := laneReport(t, r.Parent, env, 0, "path", "task", "--repo", r.Path, "--root", filepath.Join(r.Parent, "lanes")).Workspace.Path; got != wslRoot+"/task" {
		t.Errorf("WORKCOPY_PATH_STYLE=wsl: %q", got)
	}

	// A settings value in WSL spelling is understood and shapes the answer.
	settings := filepath.Join(r.Parent, "settings.env")
	if err := os.WriteFile(settings, []byte("WORKCOPY_ROOT="+wslRoot+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := laneReport(t, r.Path, []string{"WORKCOPY_SETTINGS=" + settings}, 0, "path", "task").Workspace.Path; got != wslRoot+"/task" {
		t.Errorf("a settings file written with /mnt paths: %q", got)
	}
}

func TestPathsThatCannotExistHereAreRefusedWithTheReason(t *testing.T) {
	t.Parallel()
	r := projectFixture(t, "refuse")
	foreign, code := `\\server\share\repo`, "windows-path-on-posix"
	if runtime.GOOS == "windows" {
		foreign, code = "/home/me/repo", "wsl-path-unreachable"
	}
	stdout, _, exit := run(t, r.Path, nil, "list", "--repo", foreign, "--json")
	if exit != 2 || errorCode(t, stdout) != code {
		t.Fatalf("%s: want exit 2 %s, got %d %s", foreign, code, exit, stdout)
	}
	if stdout, _, exit = run(t, r.Path, nil, "list", "--path-style", "sideways", "--json"); exit != 2 || errorCode(t, stdout) != "invalid-setting" {
		t.Fatalf("a bad --path-style: %d %s", exit, stdout)
	}
}
