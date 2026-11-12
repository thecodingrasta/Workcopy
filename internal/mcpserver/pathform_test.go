package mcpserver

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"workcopy/internal/fsops"
	"workcopy/internal/settings"
	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

func pathStyleSettings(t *testing.T, style string) *settings.Resolved {
	t.Helper()
	resolved, err := settings.Load(settings.Inputs{
		Environ: func(key string) (string, bool) {
			if key == settings.PathStyle {
				return style, style != ""
			}
			return "", false
		},
		ConfigDir: func() (string, error) { return "", os.ErrNotExist },
		ReadFile:  func(string) ([]byte, error) { return nil, os.ErrNotExist },
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// WC-022: a Windows build driven from WSL receives /mnt/c/... and must answer in
// the form it was asked in, while a native caller keeps getting native paths.
func TestWSLSpellingIsAcceptedAndAnsweredInKind(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the /mnt/<drive> spelling is translated by the Windows build; the Linux direction is covered by the interop tests")
	}
	session, first, _ := connect(t, Options{})
	second := testutil.NewRepository(t)
	second.Write("A/A.csproj", `<Project/>`)
	second.Write("A/a.txt", "second\n")
	second.Commit("second repository")
	wslRepo := fsops.Render(second.Path, fsops.FormWSL)
	wslRoot := fsops.Render(filepath.Join(second.Parent, "wsl-lanes"), fsops.FormWSL)
	if !strings.HasPrefix(wslRepo, "/mnt/") {
		t.Fatalf("test needs a drive-letter path, got %q", second.Path)
	}

	created := call(t, session, "create", map[string]any{"repo": wslRepo, "root": wslRoot, "name": "worker", "projects": []string{"A"}, "dryRun": false})
	if created.ExitCode != 0 {
		t.Fatalf("create through the WSL spelling: %+v", created.Error)
	}
	if got := created.Report.Workspace.Path; got != wslRoot+"/worker" {
		t.Fatalf("lane path must be answered in the form the caller used: %q, want %q", got, wslRoot+"/worker")
	}
	if created.Scope == nil || created.Scope.Repo != wslRepo || created.Scope.Root != wslRoot {
		t.Fatalf("the returned scope must be reusable as passed: %+v", created.Scope)
	}
	body, err := os.ReadFile(filepath.Join(second.Parent, "wsl-lanes", "worker", "A", "a.txt"))
	if err != nil || string(body) != "second\n" {
		t.Fatalf("the lane must exist at the native path: %v", err)
	}

	// The returned scope drives later calls unchanged.
	again := call(t, session, "lane_context", map[string]any{"repo": created.Scope.Repo, "root": created.Scope.Root, "name": "worker"})
	if again.ExitCode != 0 || again.Context.Repository != wslRepo {
		t.Fatalf("lane_context with the returned scope: %+v %+v", again.Error, again.Context)
	}

	// A directory is understood the same way.
	byDirectory := call(t, session, "bootstrap", Bootstrap{Directory: wslRoot + "/worker"})
	if byDirectory.ExitCode != 0 || byDirectory.Context == nil || byDirectory.Context.Repository != wslRepo || byDirectory.Context.WorkRoot != wslRoot {
		t.Fatalf("bootstrap by WSL directory: %+v %+v", byDirectory.Error, byDirectory.Context)
	}

	// A native caller on the same server is answered natively, even after the
	// WSL caller: the form belongs to the call, not to the server.
	native := call(t, session, "list", map[string]any{"repo": second.Path, "root": filepath.Join(second.Parent, "wsl-lanes")})
	if native.ExitCode != 0 || len(native.Report.Workspaces) != 1 || native.Report.Workspaces[0].Path != filepath.Join(second.Parent, "wsl-lanes", "worker") {
		t.Fatalf("a native caller must get native paths: %+v", native.Report.Workspaces)
	}
	if def := call(t, session, "bootstrap", Empty{}); def.ExitCode != 0 || def.Context.Repository != first.Path {
		t.Fatalf("a call without paths must keep the server's own form: %+v", def.Context)
	}
}

// The PathStyle setting overrides what the caller used, in both directions, and
// never rewrites conflict content.
func TestPathStyleSettingForcesTheAnswerForm(t *testing.T) {
	native, want, style := `C:\Users\me\repo`, "/mnt/c/Users/me/repo", "wsl"
	if runtime.GOOS != "windows" {
		native, want, style = "/mnt/c/Users/me/repo", `C:\Users\me\repo`, "windows"
	}
	server := New(nil, "test", Options{Settings: pathStyleSettings(t, style)})
	body := "AAAA+" + native + "/BBBB"
	out := Output{Report: &workspace.Report{Message: "lane at " + native, Content: &workspace.Content{Data: body}}}
	rendered := server.renderOutput(t.Context(), out)
	if rendered.Report.Message != "lane at "+want {
		t.Errorf("message = %q, want it to name %q", rendered.Report.Message, want)
	}
	if rendered.Report.Content.Data != body {
		t.Errorf("conflict content must never be rewritten: %q", rendered.Report.Content.Data)
	}
	if out.Report.Message != "lane at "+native {
		t.Errorf("rendering must not alter the original: %q", out.Report.Message)
	}

	native2 := New(nil, "test", Options{Settings: pathStyleSettings(t, "native")}).renderOutput(t.Context(), out)
	if native2.Report.Message != out.Report.Message {
		t.Errorf("native style must leave paths alone: %q", native2.Report.Message)
	}
}
