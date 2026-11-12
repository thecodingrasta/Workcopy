package fsops

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"workcopy/internal/model"
)

// A Windows checkout is reached by two spellings under WSL: C:\Users\me\repo
// from Windows, /mnt/c/Users/me/repo from Linux. An agent running in WSL drives
// the Windows build with the second and expects it back; a person in a Windows
// terminal drives the Linux build with the first. Workcopy accepts both at its
// edges, works internally in its own platform's form, and answers in the form the
// caller used.

// PathForm is how an absolute path is written.
type PathForm int

const (
	// FormNative is the running platform's own form.
	FormNative PathForm = iota
	// FormWSL is /mnt/c/Users/me, as seen from Linux under WSL.
	FormWSL
	// FormWindows is C:\Users\me.
	FormWindows
)

// String names the form the way the PathStyle setting does.
func (f PathForm) String() string {
	switch f {
	case FormWSL:
		return "wsl"
	case FormWindows:
		return "windows"
	}
	return "native"
}

// ParsePathStyle reads the PathStyle setting: auto, native, wsl or windows.
func ParsePathStyle(value string) (PathForm, bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return FormNative, false, nil
	case "native":
		return FormNative, true, nil
	case "wsl", "posix":
		return FormWSL, true, nil
	case "windows":
		return FormWindows, true, nil
	}
	return FormNative, false, fmt.Errorf("path style must be auto, native, wsl or windows, not %q", value)
}

var (
	windowsDrive = regexp.MustCompile(`^([A-Za-z]):(?:[\\/](.*))?$`)
	wslDrive     = regexp.MustCompile(`^/mnt/([A-Za-z])(?:/(.*))?$`)
	wslShare     = regexp.MustCompile(`(?i)^\\\\(?:wsl\$|wsl\.localhost)\\([^\\]+)(?:\\(.*))?$`)
)

// environment is what path translation needs to know about the host. It is a
// value so every platform's behaviour can be tested on any platform.
type environment struct {
	goos string
	// wsl is true when this process runs inside WSL (Linux only).
	wsl bool
	// distro is the WSL distribution name (Linux only).
	distro string
	// driveMount returns where drive letter l (upper case) is mounted (Linux only).
	driveMount func(letter string) string
	// drives lists every mount of a Windows drive, for turning a Linux path back
	// into a Windows one (Linux only). Nil falls back to /mnt/<letter>.
	drives func() []driveMountEntry
	// mountRoot is where WSL mounts drives, as the Windows build must write and
	// read it. Empty means "/mnt"; "/" means drives sit at /c, /d (Windows only).
	mountRoot string
}

func currentEnvironment() environment {
	return environment{goos: runtime.GOOS, wsl: runtime.GOOS == "linux" && underWSL(), distro: os.Getenv("WSL_DISTRO_NAME"), driveMount: driveMountPoint, drives: driveMounts, mountRoot: wslMountRoot()}
}

// underWSL reports whether this process runs inside Windows Subsystem for Linux.
func underWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	release, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(release)), "microsoft")
}

// ToNative returns path in this platform's form and the form it was written in.
// A path already in the native form is returned unchanged with FormNative.
func ToNative(path string) (string, PathForm, error) {
	return toNative(path, currentEnvironment())
}

func toNative(path string, env environment) (string, PathForm, error) {
	if path == "" {
		return path, FormNative, nil
	}
	if env.goos == "windows" {
		if match := mountedDrive(env.mountRoot).FindStringSubmatch(path); match != nil && !strings.HasPrefix(path, "//") {
			rest := strings.ReplaceAll(match[2], "/", `\`)
			return strings.ToUpper(match[1]) + `:\` + rest, FormWSL, nil
		}
		if strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") {
			return "", FormNative, model.Fail(2, "wsl-path-unreachable", fmt.Sprintf("%q is not on a Windows drive, so the Windows build cannot open it. A path inside WSL's own Linux filesystem needs the Linux build; a Windows drive is reachable as /mnt/c/... from WSL (if your drives are mounted somewhere else, set WORKCOPY_WSL_MOUNT_ROOT to that directory).", path))
		}
		return path, FormNative, nil
	}
	if match := windowsDrive.FindStringSubmatch(path); match != nil {
		if !env.wsl {
			return "", FormNative, windowsPathOnPOSIX(path, env.goos)
		}
		mount := "/mnt/" + strings.ToLower(match[1])
		if env.driveMount != nil {
			if found := env.driveMount(strings.ToUpper(match[1])); found != "" {
				mount = found
			}
		}
		return strings.TrimSuffix(mount, "/") + "/" + strings.ReplaceAll(match[2], `\`, "/"), FormWindows, nil
	}
	if match := wslShare.FindStringSubmatch(path); match != nil {
		if env.wsl && strings.EqualFold(match[1], env.distro) {
			return "/" + strings.ReplaceAll(match[2], `\`, "/"), FormWindows, nil
		}
		return "", FormNative, model.Fail(2, "windows-path-on-posix", fmt.Sprintf("%q names another WSL distribution or a network share; pass a path inside this one.", path))
	}
	if strings.HasPrefix(path, `\\`) {
		return "", FormNative, windowsPathOnPOSIX(path, env.goos)
	}
	return path, FormNative, nil
}

func windowsPathOnPOSIX(path, goos string) error {
	return model.Fail(2, "windows-path-on-posix", fmt.Sprintf("%q is a Windows path, but this Workcopy build runs on %s outside WSL, where a Windows drive does not exist. Pass a POSIX path.", path, goos))
}

// driveMountPoint finds where Linux mounted a Windows drive, from the kernel's
// mount table. It returns "" when the drive is not mounted.
func driveMountPoint(letter string) string {
	body, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	return driveMountFrom(string(body), letter)
}

// driveMountFrom is driveMountPoint over a given mount table. A drive that
// appears more than once (an extra mount of C:\) resolves to the shortest mount
// point, which is the one WSL's automount made.
func driveMountFrom(mountInfo, letter string) string {
	best := ""
	for _, line := range strings.Split(mountInfo, "\n") {
		head, tail, found := strings.Cut(line, " - ")
		if !found {
			continue
		}
		before, after := strings.Fields(head), strings.Fields(tail)
		if len(before) < 5 || len(after) < 2 {
			continue
		}
		source := strings.NewReplacer(`\134`, `\`, `\040`, " ").Replace(after[1])
		if len(source) < 2 || !strings.EqualFold(source[:2], letter+":") {
			continue
		}
		point := strings.NewReplacer(`\040`, " ", `\134`, `\`).Replace(before[4])
		if best == "" || len(point) < len(best) {
			best = point
		}
	}
	return best
}

// wslMountRoot is where WSL mounts drives unless wsl.conf says otherwise: "/mnt",
// or "/" when drives sit directly at /c, /d.
func wslMountRoot() string {
	value := strings.TrimSpace(os.Getenv(WSLMountRootEnvironment))
	if value == "" {
		return "/mnt"
	}
	if trimmed := strings.TrimRight(value, "/"); trimmed != "" {
		return trimmed
	}
	return "/"
}

// mountedDrive matches a path on a drive mounted under root ("" means "/mnt").
func mountedDrive(root string) *regexp.Regexp {
	switch root {
	case "", "/mnt":
		return wslDrive
	case "/":
		return regexp.MustCompile(`^/([A-Za-z])(?:/(.*))?$`)
	}
	return regexp.MustCompile(`^` + regexp.QuoteMeta(root) + `/([A-Za-z])(?:/(.*))?$`)
}

// WSLMountRootEnvironment names the setting for a non-default automount root,
// for a Windows build that has to write /mnt/c-style paths for WSL.
const WSLMountRootEnvironment = "WORKCOPY_WSL_MOUNT_ROOT"

// Render writes one native absolute path in the requested form. Paths that have
// no equivalent in that form are returned unchanged.
func Render(path string, form PathForm) string {
	return render(path, form, currentEnvironment(), wslMountRoot())
}

func render(path string, form PathForm, env environment, mountRoot string) string {
	switch {
	case form == FormWSL && env.goos == "windows":
		if match := windowsDrive.FindStringSubmatch(path); match != nil {
			return strings.TrimRight(mountRoot, "/") + "/" + strings.ToLower(match[1]) + "/" + strings.ReplaceAll(match[2], `\`, "/")
		}
	case form == FormWindows && env.goos != "windows":
		if letter, rest, found := driveOf(path, env); found {
			return letter + `:\` + strings.ReplaceAll(rest, "/", `\`)
		}
	}
	return path
}

// driveMountEntry is one place a Windows drive is mounted on Linux.
type driveMountEntry struct{ point, letter string }

// driveOf finds the drive a Linux path is on, preferring the most specific
// mount from the kernel's table (an extra mount of C:\ is as good as /mnt/c) and
// falling back to the /mnt/<letter> convention when the table is unavailable.
func driveOf(path string, env environment) (letter, rest string, found bool) {
	if env.drives != nil {
		best := driveMountEntry{}
		for _, entry := range env.drives() {
			if entry.point != "" && (path == entry.point || strings.HasPrefix(path, strings.TrimSuffix(entry.point, "/")+"/")) && len(entry.point) > len(best.point) {
				best = entry
			}
		}
		if best.point != "" {
			return best.letter, strings.TrimPrefix(strings.TrimPrefix(path, best.point), "/"), true
		}
	}
	if match := wslDrive.FindStringSubmatch(path); match != nil && !strings.HasPrefix(path, "//") {
		return strings.ToUpper(match[1]), match[2], true
	}
	return "", "", false
}

// driveMounts lists every Windows drive mount in the kernel's mount table.
func driveMounts() []driveMountEntry {
	body, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	return driveMountsFrom(string(body))
}

func driveMountsFrom(mountInfo string) []driveMountEntry {
	var result []driveMountEntry
	for _, line := range strings.Split(mountInfo, "\n") {
		head, tail, found := strings.Cut(line, " - ")
		if !found {
			continue
		}
		before, after := strings.Fields(head), strings.Fields(tail)
		if len(before) < 5 || len(after) < 2 {
			continue
		}
		source := strings.NewReplacer(`\134`, `\`, `\040`, " ").Replace(after[1])
		if len(source) < 2 || source[1] != ':' || !((source[0] >= 'A' && source[0] <= 'Z') || (source[0] >= 'a' && source[0] <= 'z')) {
			continue
		}
		result = append(result, driveMountEntry{point: strings.NewReplacer(`\040`, " ", `\134`, `\`).Replace(before[4]), letter: strings.ToUpper(source[:1])})
	}
	return result
}

var (
	quotedWindows = regexp.MustCompile(`(["'])([A-Za-z]:\\[^"']*)(["'])`)
	// A bare path starts at the beginning of the text or after something that
	// cannot be part of a word, so the P:\ in HTTP:\\host is not mistaken for a
	// drive. RE2 has no lookbehind, so the preceding character is captured.
	bareWindows = regexp.MustCompile(`(^|[^A-Za-z0-9_])([A-Za-z]:\\[^\s"'<>|]*)`)
	quotedMount = regexp.MustCompile(`(["'])(/mnt/[A-Za-z](?:/[^"']*)?)(["'])`)
	bareMount   = regexp.MustCompile(`(^|[^A-Za-z0-9_./])(/mnt/[A-Za-z](?:/[^\s"'<>|]*)?)`)
)

// trailingPunctuation is prose that follows a bare path in a sentence.
const trailingPunctuation = ".,;:)]}"

// RenderText converts the absolute paths found inside a message, a warning or a
// suggested command. A path in quotes is taken whole, so a directory with a
// space survives; a bare path ends at whitespace.
func RenderText(text string, form PathForm) string {
	return renderText(text, form, currentEnvironment(), wslMountRoot())
}

func renderText(text string, form PathForm, env environment, mountRoot string) string {
	convert := func(path string) string { return render(path, form, env, mountRoot) }
	var quoted, bare *regexp.Regexp
	switch {
	case form == FormWSL && env.goos == "windows":
		quoted, bare = quotedWindows, bareWindows
	case form == FormWindows && env.goos != "windows":
		quoted, bare = quotedMount, bareMount
	default:
		return text
	}
	text = quoted.ReplaceAllStringFunc(text, func(match string) string {
		groups := quoted.FindStringSubmatch(match)
		return groups[1] + convert(groups[2]) + groups[3]
	})
	return bare.ReplaceAllStringFunc(text, func(match string) string {
		groups := bare.FindStringSubmatch(match)
		trimmed := strings.TrimRight(groups[2], trailingPunctuation)
		return groups[1] + convert(trimmed) + groups[2][len(trimmed):]
	})
}

// Recorder remembers which form the caller wrote paths in, so the answer can
// use it. One recorder serves one command or one tool call.
type Recorder struct {
	mu   sync.Mutex
	seen bool
	form PathForm
}

// Note records the form of one path the caller supplied. The first foreign form
// wins; native paths are remembered only as having been seen.
func (r *Recorder) Note(form PathForm) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = true
	if form != FormNative && r.form == FormNative {
		r.form = form
	}
}

// Form is the form the caller used, or FormNative if it used only native paths.
func (r *Recorder) Form() PathForm {
	if r == nil {
		return FormNative
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.form
}

// Seen reports whether the caller supplied any path at all.
func (r *Recorder) Seen() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen
}

// Reset forgets everything recorded. Tests use it on ProcessRecorder.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen, r.form = false, FormNative
}

type recorderKey struct{}

// ProcessRecorder collects the form of paths that arrive outside any single
// command: startup flags and settings files. It is the fallback when a command
// or tool call supplied no foreign path of its own.
var ProcessRecorder = &Recorder{}

// WithRecorder returns a context carrying a fresh Recorder for one command.
func WithRecorder(ctx context.Context) (context.Context, *Recorder) {
	recorder := &Recorder{}
	return context.WithValue(ctx, recorderKey{}, recorder), recorder
}

func recorderFrom(ctx context.Context) *Recorder {
	if ctx != nil {
		if recorder, ok := ctx.Value(recorderKey{}).(*Recorder); ok {
			return recorder
		}
	}
	return ProcessRecorder
}

// NormalizeInput converts a path an outside caller supplied to this platform's
// form and remembers which form it was written in. It replaces the old refusal
// of the other platform's paths.
func NormalizeInput(ctx context.Context, path string) (string, error) {
	native, form, err := ToNative(path)
	if err != nil {
		return "", err
	}
	if path != "" {
		recorderFrom(ctx).Note(form)
	}
	return native, nil
}

// StyleFor decides how a response is written: the form the caller used in this
// command or tool call, else the form used in startup settings, unless the
// PathStyle setting names one explicitly. A call that supplied only native paths
// is answered in native paths whatever the startup settings used.
func StyleFor(ctx context.Context, setting string) PathForm {
	if form, explicit, err := ParsePathStyle(setting); err == nil && explicit {
		return form
	}
	if recorder := recorderFrom(ctx); recorder != ProcessRecorder && recorder.Seen() {
		return recorder.Form()
	}
	return ProcessRecorder.Form()
}
