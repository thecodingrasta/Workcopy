package settings

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"workcopy/internal/model"
)

// inputs builds injectable sources. file, when non-empty, is served for any
// explicit path so tests never touch the real filesystem or environment.
func inputs(body string, env map[string]string, flags map[string][]string) Inputs {
	return Inputs{
		Explicit: "",
		Environ: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
		Flags:     flags,
		ConfigDir: func() (string, error) { return "base", nil },
		ReadFile: func(string) ([]byte, error) {
			if body == "" {
				return nil, os.ErrNotExist
			}
			return []byte(body), nil
		},
		GOOS: "linux",
	}
}

func explicitPath() string {
	if runtime.GOOS == "windows" {
		return `C:\cfg\workcopy.env`
	}
	return "/cfg/workcopy.env"
}

func load(t *testing.T, body string, env map[string]string, flags map[string][]string) *Resolved {
	t.Helper()
	in := inputs(body, env, flags)
	in.Explicit = explicitPath()
	if body == "" {
		in.ReadFile = func(string) ([]byte, error) { return []byte{}, nil }
	}
	r, err := Load(in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPrecedenceIsFlagThenEnvironmentThenFileThenDefault(t *testing.T) {
	body := "WORKCOPY_TOOL_TIMEOUT=5m\nWORKCOPY_MAX_LANES=7\nWORKCOPY_RESERVE_MIB=4096\n"
	cases := []struct {
		name   string
		env    map[string]string
		flags  map[string][]string
		want   time.Duration
		source string
	}{
		{"file only", nil, nil, 5 * time.Minute, "file"},
		{"environment beats file", map[string]string{ToolTimeout: "9m"}, nil, 9 * time.Minute, "environment"},
		{"flag beats environment", map[string]string{ToolTimeout: "9m"}, map[string][]string{ToolTimeout: {"20m"}}, 20 * time.Minute, "flag"},
	}
	for _, c := range cases {
		r := load(t, body, c.env, c.flags)
		if got := r.Duration(ToolTimeout); got != c.want || r.Entry(ToolTimeout).Source != c.source {
			t.Errorf("%s: got %v from %s", c.name, got, r.Entry(ToolTimeout).Source)
		}
	}
	r := load(t, "", nil, nil)
	if r.Duration(ToolTimeout) != 2*time.Minute || r.Entry(ToolTimeout).Source != "default" || r.Overridden(ToolTimeout) {
		t.Fatalf("built-in default expected: %+v", r.Entry(ToolTimeout))
	}
	r = load(t, body, nil, nil)
	if r.Int(MaxLanes) != 7 || r.Int(ReserveMiB) != 4096 || r.Int(AgentContextMaxFiles) != 400 {
		t.Fatalf("other keys: %d %d %d", r.Int(MaxLanes), r.Int(ReserveMiB), r.Int(AgentContextMaxFiles))
	}
}

func TestEveryDefinitionIsDocumentedAndResolvable(t *testing.T) {
	r := load(t, "", nil, nil)
	seen := map[string]bool{}
	for _, d := range Definitions {
		if seen[d.Key] || d.Description == "" || !strings.HasPrefix(d.Key, "WORKCOPY_") {
			t.Errorf("bad definition %+v", d)
		}
		seen[d.Key] = true
		if r.Entry(d.Key).Description != d.Description {
			t.Errorf("%s not resolved", d.Key)
		}
		if err := validate(d, r.Entry(d.Key).Values); err != nil {
			t.Errorf("%s: own default fails validation: %v", d.Key, err)
		}
	}
	if len(r.Entries()) != len(Definitions) {
		t.Fatal("Entries must list every definition")
	}
}

func TestListsAccumulateInTheFileAndSplitOnSemicolons(t *testing.T) {
	body := "WORKCOPY_AGENT_CONTEXT_INCLUDE=docs/agents/**;.team/rules/**\nWORKCOPY_AGENT_CONTEXT_INCLUDE=extra/**\nWORKCOPY_AGENT_CONTEXT_EXCLUDE=.kiro/specs/**\n"
	r := load(t, body, nil, nil)
	if got := strings.Join(r.List(AgentContextInclude), "|"); got != "docs/agents/**|.team/rules/**|extra/**" {
		t.Fatalf("include list: %s", got)
	}
	r = load(t, body, map[string]string{AgentContextInclude: "only/**"}, nil)
	if got := strings.Join(r.List(AgentContextInclude), "|"); got != "only/**" {
		t.Fatalf("environment replaces the file list rather than extending it: %s", got)
	}
}

func TestInvalidValuesAreRejectedWithTheKeyAndTheRange(t *testing.T) {
	bad := map[string]string{
		ToolTimeout: "soon", MaxLanes: "-1", ReserveMiB: "100", IOWorkers: "0",
		JSON: "maybe", AgentContextMaxFileKiB: "99999", AgentContextInclude: "../escape/**",
	}
	if runtime.GOOS != "windows" {
		// A network share exists on no POSIX system; a drive letter would be valid under WSL.
		bad[Repo] = `\\server\share\repo`
	}
	for key, value := range bad {
		_, err := Load(inputs("", map[string]string{key: value}, nil))
		var failure *model.Failure
		if !errors.As(err, &failure) || failure.Exit != 2 || !strings.Contains(failure.Message, key) {
			t.Errorf("%s=%s: want a usage error naming the key, got %v", key, value, err)
		}
	}
	for _, value := range []string{"9s", "25h"} {
		if _, err := Load(inputs("", map[string]string{ToolTimeout: value}, nil)); err == nil {
			t.Errorf("tool timeout %s is out of range and must be rejected", value)
		}
	}
	if _, err := Load(inputs("", map[string]string{ToolTimeout: "24h"}, nil)); err != nil {
		t.Errorf("24h is allowed: %v", err)
	}
}

func TestSettingsFileSyntax(t *testing.T) {
	good := "\xef\xbb\xbf# comment\r\nexport WORKCOPY_MAX_LANES=9 # trailing\r\nOTHER_TOOL=ignored\r\nWORKCOPY_ROOT=\"/tmp/a b\"\r\nWORKCOPY_CONFIG='/tmp/c d.json'\r\n\r\n"
	values, err := ParseEnvFile([]byte(good))
	if err != nil || values[MaxLanes][0] != "9" || values[Root][0] != "/tmp/a b" || values[Config][0] != "/tmp/c d.json" || len(values) != 3 {
		t.Fatalf("%v %v", values, err)
	}
	for name, body := range map[string]string{
		"no equals":      "WORKCOPY_MAX_LANES",
		"typo":           "WORKCOPY_MAX_LANE=3",
		"open quote":     `WORKCOPY_ROOT="x`,
		"open single":    `WORKCOPY_ROOT='x`,
		"recursive file": "WORKCOPY_SETTINGS=/x",
	} {
		if _, err := ParseEnvFile([]byte(body)); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestMissingFileIsFineUnlessExplicitlyNamed(t *testing.T) {
	if _, err := Load(inputs("", nil, nil)); err != nil {
		t.Fatalf("no per-user file is the normal first run: %v", err)
	}
	named := "/nope/workcopy.env"
	if runtime.GOOS == "windows" {
		named = `C:\nope\workcopy.env`
	}
	_, err := Load(inputs("", map[string]string{FileEnvironment: named}, nil))
	var failure *model.Failure
	if !errors.As(err, &failure) || failure.Code != "settings-file-not-found" {
		t.Fatalf("a named file that is missing must be an error: %v", err)
	}
}

func TestDefaultPathPerPlatform(t *testing.T) {
	dir := func() (string, error) { return "base", nil }
	for goos, want := range map[string]string{"linux": filepath.Join("base", "workcopy", "workcopy.env"), "darwin": filepath.Join("base", "Workcopy", "workcopy.env"), "windows": filepath.Join("base", "Workcopy", "workcopy.env")} {
		if got, err := DefaultPath(dir, goos); err != nil || got != want {
			t.Errorf("%s: %s %v", goos, got, err)
		}
	}
}

func TestExportOnlyFillsWhatTheEnvironmentLacks(t *testing.T) {
	program := "/opt/git"
	if runtime.GOOS == "windows" {
		program = `C:\git\git.exe`
	}
	r := load(t, "WORKCOPY_GIT="+program+"\nWORKCOPY_IO_WORKERS=3\n", nil, nil)
	set := map[string]string{}
	environ := func(key string) (string, bool) {
		value, ok := map[string]string{IOWorkers: "8"}[key]
		return value, ok
	}
	if err := r.ExportProcessEnvironment(environ, func(k, v string) error { set[k] = v; return nil }); err != nil {
		t.Fatal(err)
	}
	if set[Git] == "" || len(set) != 1 {
		t.Fatalf("a real environment variable must win and not be overwritten: %v", set)
	}
}

func TestTemplateParsesAsEmptyAndNamesEverySetting(t *testing.T) {
	values, err := ParseEnvFile([]byte(Template()))
	if err != nil || len(values) != 0 {
		t.Fatalf("the template must be inert until edited: %v %v", values, err)
	}
	for _, d := range Definitions {
		if !strings.Contains(Template(), d.Key+"=") {
			t.Errorf("template omits %s", d.Key)
		}
	}
}

func FuzzParseEnvFile(f *testing.F) {
	for _, seed := range []string{"", "WORKCOPY_MAX_LANES=3\n", "A=B\r\n#c\n", `WORKCOPY_ROOT="a\"b"`, "export WORKCOPY_JSON=on", "\xef\xbb\xbfWORKCOPY_GIT='x'"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		values, err := ParseEnvFile(data)
		if err == nil {
			for key := range values {
				if _, known := find(key); !known {
					t.Fatalf("accepted unknown key %q", key)
				}
			}
		}
	})
}
