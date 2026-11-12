package fsops

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"workcopy/internal/model"
)

var (
	onWindows  = environment{goos: "windows"}
	onWSL      = environment{goos: "linux", wsl: true, distro: "Debian"}
	onLinux    = environment{goos: "linux"}
	onMac      = environment{goos: "darwin"}
	customMnt  = environment{goos: "linux", wsl: true, distro: "Debian", driveMount: func(l string) string { return map[string]string{"C": "/c", "D": "/mnt/data"}[l] }}
	mountTable = "31 20 0:30 / /mnt/c rw - 9p C:\\134 rw,aname=drvfs\n32 20 0:31 / /mnt/cnm rw - 9p C:\\134 rw,aname=drvfs\n33 20 0:33 / /d\\040data rw - drvfs D:\\134 rw\n"
)

func TestToNativeTranslatesBothDirectionsAndRefusesWhatCannotBeReached(t *testing.T) {
	cases := []struct {
		name string
		env  environment
		in   string
		want string
		form PathForm
		fail string
	}{
		// The Windows build, driven from WSL with the agent's own paths.
		{"windows build, wsl path", onWindows, "/mnt/c/Users/me/repo", `C:\Users\me\repo`, FormWSL, ""},
		{"windows build, drive root", onWindows, "/mnt/d", `D:\`, FormWSL, ""},
		{"windows build, lower and upper drive letters agree", onWindows, "/mnt/D/x", `D:\x`, FormWSL, ""},
		{"windows build, spaces and unicode survive", onWindows, "/mnt/c/Users/me/my repo/café", `C:\Users\me\my repo\café`, FormWSL, ""},
		{"windows build, native path untouched", onWindows, `C:\Users\me\repo`, `C:\Users\me\repo`, FormNative, ""},
		{"windows build, forward-slash drive path untouched", onWindows, "C:/Users/me/repo", "C:/Users/me/repo", FormNative, ""},
		{"windows build, relative path untouched", onWindows, `src\app`, `src\app`, FormNative, ""},
		{"windows build, UNC untouched", onWindows, `\\server\share\x`, `\\server\share\x`, FormNative, ""},
		{"windows build, wsl ext4 path cannot be opened", onWindows, "/home/me/repo", "", FormNative, "wsl-path-unreachable"},
		{"windows build, mnt without a drive letter", onWindows, "/mnt/wsl/x", "", FormNative, "wsl-path-unreachable"},
		// The Linux build under WSL, driven with Windows paths.
		{"wsl, windows path", onWSL, `C:\Users\me\repo`, "/mnt/c/Users/me/repo", FormWindows, ""},
		{"wsl, forward slashes", onWSL, "C:/Users/me/repo", "/mnt/c/Users/me/repo", FormWindows, ""},
		{"wsl, drive only", onWSL, `D:`, "/mnt/d/", FormWindows, ""},
		{"wsl, custom mount root from the mount table", customMnt, `C:\Users\me`, "/c/Users/me", FormWindows, ""},
		{"wsl, another drive's mount", customMnt, `D:\work`, "/mnt/data/work", FormWindows, ""},
		{"wsl, own distribution share", onWSL, `\\wsl.localhost\Debian\home\me\repo`, "/home/me/repo", FormWindows, ""},
		{"wsl, legacy share name", onWSL, `\\wsl$\Debian\home\me`, "/home/me", FormWindows, ""},
		{"wsl, another distribution is refused", onWSL, `\\wsl.localhost\Ubuntu\home\me`, "", FormNative, "windows-path-on-posix"},
		{"wsl, posix path untouched", onWSL, "/home/me/repo", "/home/me/repo", FormNative, ""},
		{"wsl, mnt path untouched", onWSL, "/mnt/c/Users/me", "/mnt/c/Users/me", FormNative, ""},
		// Without WSL there is no Windows drive to translate to.
		{"linux outside wsl refuses a drive", onLinux, `C:\Users\me`, "", FormNative, "windows-path-on-posix"},
		{"macOS refuses a drive", onMac, `C:\Users\me`, "", FormNative, "windows-path-on-posix"},
		{"macOS refuses UNC", onMac, `\\server\share`, "", FormNative, "windows-path-on-posix"},
		{"linux relative path untouched", onLinux, "C:relative", "C:relative", FormNative, ""},
		{"empty stays empty", onWindows, "", "", FormNative, ""},
	}
	for _, c := range cases {
		got, form, err := toNative(c.in, c.env)
		if c.fail != "" {
			if err == nil || codeOf(err) != c.fail {
				t.Errorf("%s: want error %q, got %q %v", c.name, c.fail, got, err)
			}
			continue
		}
		if err != nil || got != c.want || form != c.form {
			t.Errorf("%s: toNative(%q) = %q, %v, %v; want %q, %v", c.name, c.in, got, form, err, c.want, c.form)
		}
	}
}

func codeOf(err error) string {
	var failure *model.Failure
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}

func TestRenderWritesPathsInTheRequestedFormAndRoundTrips(t *testing.T) {
	for _, path := range []string{`C:\Users\me\repo`, `D:\x`, `C:\`, `E:\a b\c`} {
		wsl := render(path, FormWSL, onWindows, "/mnt")
		back, form, err := toNative(wsl, onWindows)
		if err != nil || form != FormWSL || !strings.EqualFold(back, strings.TrimSuffix(path, `\`)+map[bool]string{true: `\`, false: ``}[strings.HasSuffix(path, `\`)]) {
			t.Errorf("%s -> %s -> %s (%v, %v) did not round trip", path, wsl, back, form, err)
		}
	}
	if got := render(`C:\Users\me`, FormWSL, onWindows, "/mnt"); got != "/mnt/c/Users/me" {
		t.Errorf("windows to wsl: %s", got)
	}
	if got := render(`C:\Users\me`, FormWSL, onWindows, ""); got != "/c/Users/me" {
		t.Errorf("a configured automount root must be used: %s", got)
	}
	if got := render("/mnt/c/Users/me", FormWindows, onWSL, "/mnt"); got != `C:\Users\me` {
		t.Errorf("wsl to windows: %s", got)
	}
	// Forms that do not apply to the platform leave a path alone.
	for _, c := range []struct {
		path string
		form PathForm
		env  environment
	}{
		{"/home/me/x", FormWindows, onWSL}, {`C:\x`, FormWindows, onWindows}, {"/mnt/c/x", FormWSL, onWSL}, {`C:\x`, FormNative, onWindows},
	} {
		if got := render(c.path, c.form, c.env, "/mnt"); got != c.path {
			t.Errorf("render(%q, %v) changed it to %q", c.path, c.form, got)
		}
	}
}

func TestRenderTextConvertsPathsInsideMessagesAndCommands(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"quoted path with spaces", `git -C "C:\Users\me\my repo" worktree remove "C:\lanes\.workcopy-reviews\task-1234abcd"`, `git -C "/mnt/c/Users/me/my repo" worktree remove "/mnt/c/lanes/.workcopy-reviews/task-1234abcd"`},
		{"single quotes", `git -C 'C:\Users\me' status`, `git -C '/mnt/c/Users/me' status`},
		{"bare path ends at whitespace", `Workspace C:\lanes\task is ready`, `Workspace /mnt/c/lanes/task is ready`},
		{"sentence punctuation is not part of the path", `created at C:\lanes\task.`, `created at /mnt/c/lanes/task.`},
		{"comma and paren", `(see C:\lanes\a, C:\lanes\b)`, `(see /mnt/c/lanes/a, /mnt/c/lanes/b)`},
		{"relative paths are untouched", `edit src\app\main.cs and docs/readme.md`, `edit src\app\main.cs and docs/readme.md`},
		{"no paths", `nothing to convert`, `nothing to convert`},
		{"a colon that is not a drive", `error: failed at 12:34`, `error: failed at 12:34`},
		{"drive letter inside a word", `use HTTP:\\host or a:b`, `use HTTP:\\host or a:b`},
		{"a path at the very start", `C:\lanes\task is ready`, `/mnt/c/lanes/task is ready`},
		{"after an equals sign", `root=C:\lanes\task`, `root=/mnt/c/lanes/task`},
		{"after a parenthesis", `(C:\lanes\task)`, `(/mnt/c/lanes/task)`},
	}
	for _, c := range cases {
		if got := renderText(c.in, FormWSL, onWindows, "/mnt"); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
	}
	// And the other direction, from the Linux build under WSL.
	if got := renderText(`git -C "/mnt/c/Users/me/my repo" status; see /mnt/c/lanes/task.`, FormWindows, onWSL, "/mnt"); got != `git -C "C:\Users\me\my repo" status; see C:\lanes\task.` {
		t.Errorf("linux to windows text: %s", got)
	}
	// Native text is never rewritten.
	if got := renderText(`C:\lanes\task`, FormNative, onWindows, "/mnt"); got != `C:\lanes\task` {
		t.Errorf("native must not be rewritten: %s", got)
	}
}

type sample struct {
	Name    string
	Path    string
	Nested  *sample
	List    []string
	Table   map[string]string
	Any     any
	private string
}

func TestRenderCopyConvertsEveryStringAndNeverTouchesTheOriginal(t *testing.T) {
	original := &sample{
		Name: "lane", Path: `C:\lanes\task`, List: []string{`C:\a`, "relative"},
		Table:  map[string]string{`src\file.cs`: `C:\b`},
		Nested: &sample{Path: `D:\deep`}, Any: `C:\c`, private: `C:\hidden`,
	}
	environmentOverride := onWindows
	got := renderCopy(reflect.ValueOf(original), FormWSL, environmentOverride, "/mnt").Interface().(*sample)
	if got.Path != "/mnt/c/lanes/task" || got.List[0] != "/mnt/c/a" || got.List[1] != "relative" || got.Table[`src\file.cs`] != "/mnt/c/b" || got.Nested.Path != "/mnt/d/deep" || got.Any != "/mnt/c/c" {
		t.Errorf("not every string was converted: %+v nested=%+v", got, got.Nested)
	}
	if got.Name != "lane" {
		t.Errorf("a string with no path must be unchanged: %q", got.Name)
	}
	if original.Path != `C:\lanes\task` || original.List[0] != `C:\a` || original.Table[`src\file.cs`] != `C:\b` || original.Nested.Path != `D:\deep` || original.Any != `C:\c` {
		t.Errorf("the original was modified: %+v", original)
	}
	if got == original || got.Nested == original.Nested {
		t.Error("the result must be a copy")
	}
	if native := RenderCopy(original, FormNative); native != original {
		t.Error("the native form returns its input")
	}
}

func TestDriveMountFromReadsTheKernelMountTable(t *testing.T) {
	for letter, want := range map[string]string{"C": "/mnt/c", "D": "/d data", "E": ""} {
		if got := driveMountFrom(mountTable, letter); got != want {
			t.Errorf("drive %s: got %q, want %q", letter, got, want)
		}
	}
}

func TestStyleFollowsTheCallerUnlessTheSettingForcesOne(t *testing.T) {
	ctx, recorder := WithRecorder(context.Background())
	if StyleFor(ctx, "") != FormNative {
		t.Error("with no foreign path the style is native")
	}
	recorder.Note(FormWSL)
	recorder.Note(FormWindows) // the first foreign form wins
	if StyleFor(ctx, "auto") != FormWSL {
		t.Error("the style follows the form the caller wrote")
	}
	if StyleFor(ctx, "windows") != FormWindows || StyleFor(ctx, "native") != FormNative || StyleFor(ctx, "wsl") != FormWSL {
		t.Error("an explicit setting overrides what the caller wrote")
	}
	if _, _, err := ParsePathStyle("sideways"); err == nil {
		t.Error("an unknown style must be rejected")
	}
	other, _ := WithRecorder(context.Background())
	if StyleFor(other, "") != FormNative {
		t.Error("recorders are per command and must not leak between them")
	}
}

// WSL's automount root can be moved ([automount] root=/ or /drives), and a drive
// can be mounted a second time somewhere else. Both are common enough that a
// hard-coded /mnt would break the people who have them.
func TestMovedAutomountRootIsHonouredInBothDirections(t *testing.T) {
	cases := []struct {
		name, in, want, root string
	}{
		{"drives at /", "/c/work/repo", `C:\work\repo`, "/"},
		{"drives under /drives", "/drives/d/x", `D:\x`, "/drives"},
		{"default root still understood when unset", "/mnt/c/work", `C:\work`, ""},
	}
	for _, c := range cases {
		got, form, err := toNative(c.in, environment{goos: "windows", mountRoot: c.root})
		if err != nil || got != c.want || form != FormWSL {
			t.Errorf("%s: toNative(%q) = %q, %v, %v; want %q", c.name, c.in, got, form, err, c.want)
		}
		back := render(got, FormWSL, environment{goos: "windows"}, c.root)
		if c.root != "" && back != c.in {
			t.Errorf("%s: render(%q) = %q, want the spelling it came from, %q", c.name, got, back, c.in)
		}
	}
	// The default root no longer swallows a path that is merely shaped like it.
	if _, _, err := toNative("/c/work", environment{goos: "windows", mountRoot: "/drives"}); err == nil {
		t.Error("with drives under /drives, /c/work is not a drive path")
	}
	if _, _, err := toNative("/mnt/c/work", environment{goos: "windows", mountRoot: "/drives"}); err == nil {
		t.Error("with drives under /drives, /mnt/c is not a drive path")
	}
}

func TestLinuxPathsOnAnyMountOfADriveBecomeWindowsPaths(t *testing.T) {
	table := "36 1 8:1 / / rw - ext4 /dev/sdc rw\n" +
		"90 36 0:50 / /mnt/c rw,noatime - 9p C:\\134 rw,aname=drvfs;path=C:\\134\n" +
		"91 36 0:51 / /mnt/cnm rw - 9p C:\\134 rw\n" +
		"92 36 0:52 / /mnt/data rw - 9p D:\\134 rw\n" +
		"93 36 0:53 / /mnt/wsl rw - tmpfs tmpfs rw\n"
	drives := driveMountsFrom(table)
	if len(drives) != 3 {
		t.Fatalf("expected the three drive mounts and not the ext4 root or tmpfs, got %+v", drives)
	}
	env := environment{goos: "linux", wsl: true, drives: func() []driveMountEntry { return drives }}
	for in, want := range map[string]string{
		"/mnt/c/Users/me":     `C:\Users\me`,
		"/mnt/cnm/Users/me":   `C:\Users\me`, // the second mount of C:\ is as good as the first
		"/mnt/data/projects":  `D:\projects`,
		"/mnt/data":           `D:\`,
		"/mnt/cnmx/not-mount": "/mnt/cnmx/not-mount", // a name that merely starts like a mount point
		"/home/me":            "/home/me",
		"/mnt/wsl/x":          "/mnt/wsl/x",
	} {
		if got := render(in, FormWindows, env, "/mnt"); got != want {
			t.Errorf("render(%q) = %q, want %q", in, got, want)
		}
	}
}
