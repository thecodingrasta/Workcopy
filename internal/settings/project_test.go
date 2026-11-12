package settings

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
)

func absolute(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `C:\work\` + name
	}
	return "/work/" + name
}

func TestProjectFileSitsBetweenTheUserFileAndTheEnvironment(t *testing.T) {
	user, project, environment, flag := absolute(t, "user"), absolute(t, "project"), absolute(t, "env"), absolute(t, "flag")
	file := filepath.Join(absolute(t, "repo.git"), ProjectFileName)
	body := []byte("WORKCOPY_ROOT=" + project + "\nWORKCOPY_AGENT_CONTEXT_MAX_FILES=50\n")

	cases := []struct {
		name  string
		env   map[string]string
		flags map[string][]string
		want  string
		from  string
	}{
		{"project beats the user file", nil, nil, project, "project"},
		{"environment beats the project file", map[string]string{"WORKCOPY_ROOT": environment}, nil, environment, "environment"},
		{"a flag beats everything", map[string]string{"WORKCOPY_ROOT": environment}, map[string][]string{Root: {flag}}, flag, "flag"},
	}
	for _, c := range cases {
		base := load(t, "WORKCOPY_ROOT="+user+"\n", c.env, c.flags)
		layered, err := base.WithProject(file, body)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := layered.String(Root); got != c.want || layered.Entry(Root).Source != c.from {
			t.Errorf("%s: root = %q from %s, want %q from %s", c.name, got, layered.Entry(Root).Source, c.want, c.from)
		}
		if layered.Int(AgentContextMaxFiles) != 50 {
			t.Errorf("%s: the project's other keys apply too", c.name)
		}
		// The receiver is shared by callers that outlive the call; it must not move.
		if base.String(Root) == layered.String(Root) && c.from == "project" {
			t.Errorf("%s: the user-level settings were modified", c.name)
		}
		if layered.ProjectFile != file {
			t.Errorf("%s: the applied file must be recorded", c.name)
		}
	}
	// Absent from the project file, a key keeps the lower layer's value.
	base := load(t, "WORKCOPY_ROOT="+user+"\nWORKCOPY_AGENT_CONTEXT_MAX_TOTAL_KIB=111\n", nil, nil)
	layered, err := base.WithProject(file, body)
	if err != nil || layered.Int(AgentContextMaxKiB) != 111 || layered.Entry(AgentContextMaxKiB).Source != "file" {
		t.Fatalf("untouched keys must pass through: %v %+v", err, layered.Entry(AgentContextMaxKiB))
	}
}

func TestProjectFileRefusesWhatOnlyTheUserMaySet(t *testing.T) {
	file := filepath.Join(absolute(t, "repo.git"), ProjectFileName)
	base := load(t, "WORKCOPY_TOOL_TIMEOUT=5m\n", nil, nil)
	for name, body := range map[string]string{
		"git program":     "WORKCOPY_GIT=/bin/true",
		"share targets":   "WORKCOPY_ALLOW_SHARE_TARGET=/tmp",
		"mcp limit":       "WORKCOPY_MAX_LANES=1",
		"output form":     "WORKCOPY_JSON=on",
		"relative root":   "WORKCOPY_ROOT=lanes",
		"relative config": "WORKCOPY_CONFIG=../owner.json",
		"unknown key":     "WORKCOPY_NOT_A_SETTING=1",
		"bad number":      "WORKCOPY_IO_WORKERS=many",
		"out of range":    "WORKCOPY_AGENT_CONTEXT_MAX_FILES=0",
		"not a setting":   "this is not KEY=VALUE\nnor this",
	} {
		_, err := base.WithProject(file, []byte(body))
		var failure *model.Failure
		if err == nil || !asFailure(err, &failure) || failure.Code != "invalid-settings-file" || failure.Exit != 2 || !strings.Contains(failure.Message, file) {
			t.Errorf("%s: want invalid-settings-file naming the file, got %v", name, err)
		}
	}
	// Keys other tools put in a shared dotenv file are not Workcopy's business.
	if _, err := base.WithProject(file, []byte("PATH_SOMETHING=1\n# a comment\n\nWORKCOPY_IO_WORKERS=2\n")); err != nil {
		t.Errorf("foreign keys and comments must be ignored: %v", err)
	}
}

func asFailure(err error, target **model.Failure) bool {
	failure, ok := err.(*model.Failure)
	*target = failure
	return ok
}

func TestProjectKeysAreTheSafeSubset(t *testing.T) {
	for _, d := range Definitions {
		switch d.Key {
		case Git, AllowShareTarget, Repo, MaxLanes, ReserveMiB, ToolTimeout, JSON, PathStyle, WSLMountRoot:
			if ProjectKey(d.Key) {
				t.Errorf("%s must stay with the user's own settings", d.Key)
			}
		case Root, Config, MaxFileMiB:
			if !ProjectKey(d.Key) {
				t.Errorf("%s must be settable per repository", d.Key)
			}
		}
	}
	template := ProjectTemplate()
	for _, d := range Definitions {
		if ProjectKey(d.Key) != strings.Contains(template, "# "+d.Key+"=") {
			t.Errorf("template and key set disagree about %s", d.Key)
		}
	}
	if values, err := ParseEnvFile([]byte(template)); err != nil || len(values) != 0 {
		t.Errorf("the untouched template must parse as empty: %v %v", err, values)
	}
}

// The project file may be written with the other platform's spelling of a path,
// like every other setting, and is read in this platform's form.
func TestPathSettingsAcceptEitherSpelling(t *testing.T) {
	defer fsops.ProcessRecorder.Reset()
	if runtime.GOOS != "windows" {
		t.Skip("the Windows build translates /mnt/<drive>; the Linux direction needs WSL and is covered by the interop tests")
	}
	r := load(t, "WORKCOPY_ROOT=/mnt/c/lanes\n", nil, nil)
	if got := r.String(Root); got != `C:\lanes` {
		t.Errorf("root = %q, want C:\\lanes", got)
	}
	if fsops.ProcessRecorder.Form() != fsops.FormWSL {
		t.Error("the spelling the user chose must be remembered for the answers")
	}
	if _, err := Load(func() Inputs {
		in := inputs("WORKCOPY_ROOT=/home/me/lanes\n", nil, nil)
		in.Explicit = explicitPath()
		return in
	}()); err == nil || !strings.Contains(err.Error(), "WORKCOPY_ROOT") {
		t.Errorf("a path the Windows build cannot reach must be refused naming the setting: %v", err)
	}
}

func TestPathStyleSettingIsValidated(t *testing.T) {
	for _, good := range []string{"auto", "native", "wsl", "windows", "WSL"} {
		in := inputs("", map[string]string{PathStyle: good}, nil)
		if _, err := Load(in); err != nil {
			t.Errorf("%s: %v", good, err)
		}
	}
	if _, err := Load(inputs("", map[string]string{PathStyle: "sideways"}, nil)); err == nil || !strings.Contains(err.Error(), PathStyle) {
		t.Errorf("a bad style must be refused naming the setting: %v", err)
	}
}
