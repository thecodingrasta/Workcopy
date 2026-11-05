// Package interop checks the hybrid setup that motivated most of the v0.1.3
// field reports: a Windows host with Git for Windows and a WSL distribution
// with its own Linux Git, where either side may drive a repository that lives
// on the other's filesystem.
//
// Both directions run the real binaries of both operating systems against real
// repositories. They need a machine with WSL, so they skip everywhere else; the
// hosted CI runners have no WSL distribution. Run them on a developer machine:
//
//	go test ./tests/interop -v                       (from Windows)
//	go test ./tests/interop -v                       (from inside WSL, with Windows interop on)
//
// WORKCOPY_INTEROP_DISTRO selects the distribution (default Debian).
package interop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

func TestMain(m *testing.M) {
	cleanup := testutil.IsolateSettings()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func distro() string {
	if value := os.Getenv("WORKCOPY_INTEROP_DISTRO"); value != "" {
		return value
	}
	return "Debian"
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for depth := 0; depth < 4; depth++ {
		if body, readErr := os.ReadFile(filepath.Join(directory, "go.mod")); readErr == nil && strings.HasPrefix(string(body), "module workcopy") {
			return directory
		}
		directory = filepath.Dir(directory)
	}
	t.Fatal("cannot find the Workcopy source root")
	return ""
}

func run(t *testing.T, name string, env []string, dir string, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	command.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	code := 0
	if err := command.Run(); err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%s %v: %v", name, args, err)
		}
		code = exit.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func build(t *testing.T, goos, output string) {
	t.Helper()
	out, errText, code := run(t, "go", []string{"GOOS=" + goos, "GOARCH=amd64", "CGO_ENABLED=0"}, repositoryRoot(t), "build", "-trimpath", "-buildvcs=false", "-o", output, "./cmd/workcopy")
	if code != 0 {
		t.Fatalf("cross-compiling for %s failed: %s %s", goos, out, errText)
	}
}

func decode(t *testing.T, stdout string) workspace.Report {
	t.Helper()
	var report workspace.Report
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	return report
}

const attributes = "*.cs text eol=crlf\n*.sh text eol=lf\n*.txt text eol=lf\n*.dat -text\n"

// interopRepository holds files whose working-tree form is fixed by attributes
// alone, so every Workcopy build on every platform must write identical bytes.
func interopRepository(t *testing.T, r *testutil.Repository) {
	t.Helper()
	r.Write("src/Big.cs", strings.Repeat("class C {}\n", 30))
	r.Write("src/run.sh", "#!/bin/sh\necho hi\n")
	r.Write("docs/readme.txt", "one\ntwo\n")
	r.Write("data/blob.dat", "a\r\nb\n")
	r.Commit("content")
	r.Write(".gitattributes", attributes)
	r.Git("add", ".gitattributes")
	r.Git("commit", "-m", "attributes")
}

func treeBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative == ".workcopy" {
				return filepath.SkipDir
			}
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		result[relative] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// --- Windows host driving WSL -------------------------------------------------

func wsl(t *testing.T, args ...string) (string, int) {
	t.Helper()
	out, _, code := run(t, "wsl.exe", nil, "", append([]string{"-d", distro(), "--exec"}, args...)...)
	return strings.TrimSpace(strings.ReplaceAll(out, "\x00", "")), code
}

// TestLinuxBuildInWSLOnAWindowsRepository: the Linux binary and Linux Git, run
// inside WSL against a repository on a Windows drive (the arrangement WC-001,
// WC-002 and WC-006 were reported against), alongside the Windows build.
func TestLinuxBuildInWSLOnAWindowsRepository(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("this direction starts on the Windows host")
	}
	if _, code := wsl(t, "true"); code != 0 {
		t.Skipf("no WSL distribution %q is available", distro())
	}
	gitExe, err := exec.LookPath("git.exe")
	if err != nil {
		t.Skip("Git for Windows is not installed")
	}
	temp := t.TempDir()
	linuxBinary := filepath.Join(temp, "workcopy-linux")
	windowsBinary := filepath.Join(temp, "workcopy.exe")
	build(t, "linux", linuxBinary)
	build(t, "windows", windowsBinary)
	toLinux := func(windowsPath string) string {
		out, code := wsl(t, "wslpath", "-u", windowsPath)
		if code != 0 || out == "" {
			t.Fatalf("wslpath %s: %d %s", windowsPath, code, out)
		}
		return out
	}

	repo := testutil.NewRepository(t)
	interopRepository(t, repo)
	linuxRepo, linuxRoot := toLinux(repo.Path), toLinux(filepath.Join(repo.Parent, "linux-lanes"))
	// The Linux binary runs from the WSL filesystem, as a real install would.
	installed := "/tmp/workcopy-interop"
	if out, code := wsl(t, "sh", "-c", fmt.Sprintf("cp %q %s && chmod +x %s", toLinux(linuxBinary), installed, installed)); code != 0 {
		t.Fatalf("installing the Linux binary in WSL: %d %s", code, out)
	}
	linux := func(arguments ...string) (string, int) {
		out, _, code := run(t, "wsl.exe", nil, "", append([]string{"-d", distro(), "--exec", "env", "WORKCOPY_SETTINGS=/dev/null", installed}, arguments...)...)
		return out, code
	}

	// 1. The Linux build creates a lane from the Windows repository.
	out, code := linux("create", "lx", "--all", "--repo", linuxRepo, "--root", linuxRoot, "--json")
	if code != 0 {
		t.Fatalf("Linux create on a Windows drive: %d %s", code, out)
	}
	if decode(t, out).Workspace == nil {
		t.Fatalf("Linux create returned no workspace: %s", out)
	}

	// 2. The Windows build creates a lane from the same repository.
	out, _, code = run(t, windowsBinary, nil, repo.Path, "create", "win", "--all", "--repo", repo.Path, "--root", filepath.Join(repo.Parent, "win-lanes"), "--json")
	if code != 0 {
		t.Fatalf("Windows create: %d %s", code, out)
	}
	windowsLane := decode(t, out).Workspace.Path

	// Same repository, attribute-fixed forms: both builds must write the same bytes.
	fromLinux := treeBytes(t, filepath.Join(repo.Parent, "linux-lanes", "lx"))
	fromWindows := treeBytes(t, windowsLane)
	if len(fromLinux) == 0 || len(fromLinux) != len(fromWindows) {
		t.Fatalf("file sets differ: %d vs %d", len(fromLinux), len(fromWindows))
	}
	for name, want := range fromWindows {
		if fromLinux[name] != want {
			t.Errorf("%s: the Linux and Windows builds wrote different bytes:\nlinux   %q\nwindows %q", name, fromLinux[name], want)
		}
	}
	if !strings.Contains(fromLinux["src/Big.cs"], "\r\n") || strings.Contains(fromLinux["docs/readme.txt"], "\r") || fromLinux["data/blob.dat"] != "a\r\nb\n" {
		t.Fatalf("line endings must follow the attributes on both builds: %q %q", fromLinux["src/Big.cs"][:20], fromLinux["data/blob.dat"])
	}

	// 3. Linux Git on a Windows drive: a review is created, and Windows Git
	// reports the limitation WC-002 described. doctor must warn about it.
	if err = os.WriteFile(filepath.Join(repo.Parent, "linux-lanes", "lx", "docs", "readme.txt"), []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out, code = linux("merge", "lx", "--repo", linuxRepo, "--root", linuxRoot, "--json")
	if code != 0 {
		t.Fatalf("Linux merge on a Windows drive: %d %s", code, out)
	}
	review := decode(t, out).Review
	listing, _, _ := run(t, gitExe, nil, repo.Path, "worktree", "list", "--porcelain")
	if !strings.Contains(listing, "prunable") {
		t.Fatalf("the documented limitation did not reproduce: a review made by Linux Git should be prunable to Windows Git:\n%s", listing)
	}
	out, code = linux("doctor", "--repo", linuxRepo, "--root", linuxRoot, "--json")
	doctor := decode(t, out)
	if code != 0 || !strings.Contains(strings.Join(doctor.Warnings, "\n"), "Windows drive seen from WSL") {
		t.Fatalf("doctor must warn about the Linux build on a Windows drive: %d %s", code, out)
	}
	if review == nil {
		t.Fatal("the Linux merge returned no review")
	}

	// 4. The same operation by the Windows build leaves a worktree Windows Git can use.
	if err = os.WriteFile(filepath.Join(windowsLane, "docs", "readme.txt"), []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out, _, code = run(t, windowsBinary, nil, repo.Path, "merge", "win", "--repo", repo.Path, "--root", filepath.Join(repo.Parent, "win-lanes"), "--json")
	if code != 0 {
		t.Fatalf("Windows merge: %d %s", code, out)
	}
	winReview := decode(t, out).Review
	listing, _, _ = run(t, gitExe, nil, repo.Path, "worktree", "list", "--porcelain")
	for _, block := range strings.Split(listing, "\n\n") {
		if strings.Contains(block, filepath.Base(winReview.Path)) && strings.Contains(block, "prunable") {
			t.Fatalf("a review made by the Windows build must be healthy for Windows Git:\n%s", block)
		}
	}
	if status, _, statusCode := run(t, gitExe, nil, winReview.Path, "status", "--porcelain"); statusCode != 0 || strings.TrimSpace(status) != "M docs/readme.txt" {
		t.Fatalf("Windows Git must see exactly the one change in the Windows review: %d %q", statusCode, status)
	}

	// 5. WORKCOPY_GIT is honoured and fails closed.
	out, _, _ = run(t, "wsl.exe", nil, "", "-d", distro(), "--exec", "env", "WORKCOPY_SETTINGS=/dev/null", "WORKCOPY_GIT=/usr/bin/git", installed, "doctor", "--repo", linuxRepo, "--json")
	if !strings.Contains(out, "WORKCOPY_GIT=/usr/bin/git") {
		t.Fatalf("doctor should report the chosen Git program: %s", out)
	}
	out, _, code = run(t, "wsl.exe", nil, "", "-d", distro(), "--exec", "env", "WORKCOPY_SETTINGS=/dev/null", "WORKCOPY_GIT=/no/such/git", installed, "doctor", "--json")
	if code != 3 || !strings.Contains(out, "git-unavailable") {
		t.Fatalf("a bad WORKCOPY_GIT must fail closed: %d %s", code, out)
	}

	// 6. Foreign paths get the actionable message from each build.
	if out, _, code = run(t, windowsBinary, nil, repo.Path, "list", "--repo", linuxRepo, "--json"); code != 2 || !strings.Contains(out, "posix-path-on-windows") {
		t.Fatalf("the Windows build given a /mnt path: %d %s", code, out)
	}
	if out, code = linux("list", "--repo", repo.Path, "--json"); code != 2 || !strings.Contains(out, "windows-path-on-posix") {
		t.Fatalf("the Linux build given a C:\\ path: %d %s", code, out)
	}
}

// --- WSL driving the Windows build ---------------------------------------------

// TestWindowsBuildFromInsideWSL: the owner's working arrangement. Kiro runs in
// WSL and drives workcopy.exe through interop with Windows paths, on a
// repository that lives on NTFS.
func TestWindowsBuildFromInsideWSL(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("this direction starts inside WSL")
	}
	if release, err := os.ReadFile("/proc/sys/kernel/osrelease"); err != nil || !strings.Contains(strings.ToLower(string(release)), "microsoft") {
		t.Skip("not running under WSL")
	}
	if _, err := exec.LookPath("cmd.exe"); err != nil {
		t.Skip("Windows interop is disabled")
	}
	gitExe, err := exec.LookPath("git.exe")
	if err != nil {
		t.Skip("Git for Windows is not reachable from WSL")
	}
	wslpath := func(flag, path string) string {
		out, _, code := run(t, "wslpath", nil, "", flag, path)
		if code != 0 {
			t.Fatalf("wslpath %s %s", flag, path)
		}
		return strings.TrimSpace(out)
	}
	profile, _, _ := run(t, "cmd.exe", nil, "/mnt/c", "/c", "echo", "%LOCALAPPDATA%")
	temp := filepath.Join(wslpath("-u", strings.TrimSpace(profile)), "Temp", fmt.Sprintf("wc-interop-%d", time.Now().UnixNano()))
	if err = os.MkdirAll(temp, 0755); err != nil {
		t.Skipf("no writable Windows temp directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(temp) })
	exe := filepath.Join(temp, "workcopy.exe")
	build(t, "windows", exe)

	repo := filepath.Join(temp, "repo")
	if err = os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		out, errText, code := run(t, gitExe, []string{"GIT_CONFIG_NOSYSTEM=1"}, repo, args...)
		if code != 0 {
			t.Fatalf("git.exe %v: %d %s %s", args, code, out, errText)
		}
		return strings.TrimSpace(out)
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "Interop")
	git("config", "user.email", "interop@example.invalid")
	git("config", "core.autocrlf", "false")
	write := func(name, body string) {
		full := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/Big.cs", strings.Repeat("class C {}\n", 30))
	write("docs/readme.txt", "one\ntwo\n")
	write("AGENTS.md", "tracked policy\n")
	git("add", "-A")
	git("commit", "-q", "-m", "content")
	write(".gitattributes", attributes)
	git("add", ".gitattributes")
	git("commit", "-q", "-m", "attributes")
	write(".kiro/steering/product.md", "untracked steering\n")
	write(".kiro/settings/mcp.json", `{"env":{"TOKEN":"abc"}}`)

	winRepo, winRoot := wslpath("-w", repo), wslpath("-w", filepath.Join(temp, "lanes"))
	exeIn := func(env []string, args ...string) (string, int) {
		out, _, code := run(t, exe, env, repo, args...)
		return out, code
	}
	if err = os.WriteFile(filepath.Join(temp, "empty.env"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	// WSLENV forwards the variable to the Windows process, translating the path.
	env := []string{"WSLENV=WORKCOPY_SETTINGS/p", "WORKCOPY_SETTINGS=" + filepath.Join(temp, "empty.env")}

	// A POSIX path to the .exe is the WC-012 trap; Windows paths work.
	if out, code := exeIn(env, "list", "--repo", repo, "--json"); code != 2 || !strings.Contains(out, "posix-path-on-windows") {
		t.Fatalf("a /mnt path given to the .exe: %d %s", code, out)
	}
	out, code := exeIn(env, "create", "l1", "--all", "--repo", winRepo, "--root", winRoot, "--json")
	if code != 0 {
		t.Fatalf("create through interop: %d %s", code, out)
	}
	created := decode(t, out)
	lane := wslpath("-u", created.Workspace.Path)
	if body, _ := os.ReadFile(filepath.Join(lane, "src", "Big.cs")); !bytes.Contains(body, []byte("\r\n")) {
		t.Fatal("eol=crlf must give CRLF in the lane")
	}
	if created.AgentContext == nil || strings.Join(created.AgentContext.Added, ",") != ".kiro/steering/product.md" {
		t.Fatalf("agent context through interop: %+v", created.AgentContext)
	}
	if _, err = os.Stat(filepath.Join(lane, ".kiro", "settings")); err == nil {
		t.Fatal("tool settings must not be copied")
	}
	// Edit from the Linux side (as an agent in WSL would), then review.
	if err = os.WriteFile(filepath.Join(lane, "docs", "readme.txt"), []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, code = exeIn(env, "status", "l1", "--repo", winRepo, "--root", winRoot, "--json"); code != 0 || !strings.Contains(out, "docs/readme.txt") || strings.Contains(out, ".kiro") {
		t.Fatalf("status after a WSL-side edit: %d %s", code, out)
	}
	out, code = exeIn(env, "merge", "l1", "--repo", winRepo, "--root", winRoot, "--json")
	if code != 0 {
		t.Fatalf("merge through interop: %d %s", code, out)
	}
	review := decode(t, out).Review
	listing := git("worktree", "list", "--porcelain")
	if strings.Contains(listing, "prunable") {
		t.Fatalf("a review made by workcopy.exe must be healthy for Windows Git:\n%s", listing)
	}
	if status, _, statusCode := run(t, gitExe, nil, repo, "-C", review.Path, "status", "--porcelain"); statusCode != 0 || strings.TrimSpace(status) != "M docs/readme.txt" {
		t.Fatalf("Windows Git status in the review: %d %q", statusCode, status)
	}
	// Linux Git can read the repository (history), proving both Gits coexist on it.
	if out, _, code = run(t, "git", []string{"GIT_CONFIG_NOSYSTEM=1"}, repo, "-c", "safe.directory=*", "log", "--oneline"); code != 0 || strings.Count(out, "\n") < 2 {
		t.Fatalf("Linux Git reading the Windows repository: %d %s", code, out)
	}
	// The review is out; restore the lane's content so cleanup needs no --force.
	if err = os.WriteFile(filepath.Join(lane, "docs", "readme.txt"), []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Cleanup works, keeps the review, and prints the worktree command.
	out, code = exeIn(env, "destroy", "l1", "--repo", winRepo, "--root", winRoot, "--json")
	if code != 0 {
		t.Fatalf("destroy through interop: %d %s", code, out)
	}
	if _, err = os.Stat(wslpath("-u", review.Path)); err != nil {
		t.Fatal("destroy must keep the review worktree")
	}
}
