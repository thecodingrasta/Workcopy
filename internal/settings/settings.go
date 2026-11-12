// Package settings resolves every user-tunable Workcopy default from one place.
//
// A value comes from, in order of precedence: a command-line flag, the process
// environment, a settings file in dotenv format (KEY=VALUE lines), and finally
// the built-in default. The same keys work in all three places, so a user sets
// a preference once in the file and never repeats it on a command line.
package settings

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"workcopy/internal/agents"
	"workcopy/internal/fsops"
	"workcopy/internal/model"
)

// Keys. Each is also accepted as an environment variable of the same name.
const (
	Repo             = "WORKCOPY_REPO"
	Root             = "WORKCOPY_ROOT"
	Config           = "WORKCOPY_CONFIG"
	MaxLanes         = "WORKCOPY_MAX_LANES"
	ReserveMiB       = "WORKCOPY_RESERVE_MIB"
	ToolTimeout      = "WORKCOPY_TOOL_TIMEOUT"
	AllowShareTarget = "WORKCOPY_ALLOW_SHARE_TARGET"
	Git              = "WORKCOPY_GIT"
	IOWorkers        = "WORKCOPY_IO_WORKERS"
	MaxFileMiB       = model.MaxFileMiBKey
	PathStyle        = "WORKCOPY_PATH_STYLE"
	WSLMountRoot     = fsops.WSLMountRootEnvironment
	JSON             = "WORKCOPY_JSON"

	Agents                 = "WORKCOPY_AGENTS"
	AgentContext           = "WORKCOPY_AGENT_CONTEXT"
	AgentContextDetect     = "WORKCOPY_AGENT_CONTEXT_DETECT"
	AgentContextInclude    = "WORKCOPY_AGENT_CONTEXT_INCLUDE"
	AgentContextExclude    = "WORKCOPY_AGENT_CONTEXT_EXCLUDE"
	AgentContextConfig     = "WORKCOPY_AGENT_CONTEXT_CONFIG"
	AgentContextSecrets    = "WORKCOPY_AGENT_CONTEXT_SECRETS"
	AgentContextMaxFileKiB = "WORKCOPY_AGENT_CONTEXT_MAX_FILE_KIB"
	AgentContextMaxKiB     = "WORKCOPY_AGENT_CONTEXT_MAX_TOTAL_KIB"
	AgentContextMaxFiles   = "WORKCOPY_AGENT_CONTEXT_MAX_FILES"
	AgentContextOnSync     = "WORKCOPY_AGENT_CONTEXT_REFRESH_ON_SYNC"

	// FileEnvironment names the settings file when no --settings flag is given.
	FileEnvironment = "WORKCOPY_SETTINGS"
)

type kind int

const (
	text kind = iota
	path
	number
	duration
	boolean
	list
)

// Definition describes one setting.
type Definition struct {
	Key, Flag, Default, Description string
	kind                            kind
	min, max                        int64
}

// Definitions lists every setting in display order.
var Definitions = []Definition{
	{Key: Repo, Flag: "repo", kind: path, Description: "Default source checkout when --repo is not given and the current directory is not inside a lane"},
	{Key: Root, Flag: "root", kind: path, Description: "Where lanes are stored; defaults to a directory beside the checkout"},
	{Key: Config, Flag: "config", kind: path, Description: "Owner configuration file (projects, profiles, destroy.discard); treated as owner-supplied"},
	{Key: MaxLanes, Flag: "max-lanes", kind: number, Default: "32", min: 0, max: 100000, Description: "MCP: lane count limit per repository and root; 0 disables it"},
	{Key: ReserveMiB, Flag: "reserve-mib", kind: number, Default: "2048", min: 2048, max: 1 << 20, Description: "MCP: free disk space to keep in reserve, in MiB"},
	{Key: ToolTimeout, Flag: "tool-timeout", kind: duration, Default: "2m", min: int64(10 * time.Second), max: int64(24 * time.Hour), Description: "MCP: deadline for a single tool call (10s to 24h, for example 15m)"},
	{Key: AllowShareTarget, Flag: "allow-share-target", kind: list, Description: "MCP: directories an agent may link with share; separate several with ;"},
	{Key: Git, kind: path, Description: "Git program to use instead of the first git on PATH"},
	{Key: IOWorkers, kind: number, min: 1, max: 64, Description: "Concurrent file operations for bulk work; 1 is strictly sequential"},
	{Key: MaxFileMiB, kind: number, Default: "2048", min: 64, max: 1 << 20, Description: "Largest tracked file Workcopy will copy into a lane, in MiB. Files over 64 MiB are streamed, treated as binary and never merged as text, so size costs time, not memory"},
	{Key: PathStyle, Flag: "path-style", kind: text, Default: "auto", Description: "How lane and repository paths are written in answers: auto (the form you passed, so /mnt/c/... in gives /mnt/c/... out), native, wsl (/mnt/c/...) or windows (C:\\...). Workcopy accepts both forms as input either way"},
	{Key: WSLMountRoot, kind: text, Description: "Windows build only: the directory where WSL mounts Windows drives, when /etc/wsl.conf moved it from /mnt (use / for drives at /c, /d). Used to read and to write /mnt/c-style paths"},
	{Key: JSON, Flag: "json", kind: boolean, Default: "off", Description: "CLI: print JSON by default"},

	{Key: Agents, Flag: "agent", kind: list, Default: "auto", Description: "Which agent tools a lane serves: auto (the MCP client decides, everything when unknown), all, or tool keys such as kiro;codex;claude;gemini;opencode;cursor. Tool-neutral files (AGENTS.md) are always copied"},
	{Key: AgentContext, kind: boolean, Default: "on", Description: "Copy agent instruction files (rules, steering, prompts, skills) into every lane"},
	{Key: AgentContextDetect, kind: boolean, Default: "on", Description: "Recognise the built-in catalogue of agent tools (.kiro, .claude, .codex, .gemini, .cursor, ...)"},
	{Key: AgentContextInclude, kind: list, Description: "Extra repository-relative globs to copy, for example docs/agents/**; separate several with ;"},
	{Key: AgentContextExclude, kind: list, Description: "Repository-relative globs never to copy, applied after detection and includes; separate several with ;"},
	{Key: AgentContextConfig, kind: list, Description: "Opt in tool configuration files (MCP definitions, tool settings) to copy into every lane, for example .kiro/settings/mcp.json. Credentials stay refused; only the owner can set this; separate several with ;"},
	{Key: AgentContextSecrets, kind: list, Description: "Exact files (no wildcards) you accept may contain credentials and want copied into every lane, for example .tools/.env. Key files and cloud credential stores are still refused; separate several with ;"},
	{Key: AgentContextMaxFileKiB, kind: number, Default: "256", min: 1, max: 4096, Description: "Largest single agent-context file, in KiB"},
	{Key: AgentContextMaxKiB, kind: number, Default: "8192", min: 1, max: 262144, Description: "Total agent-context size per lane, in KiB"},
	{Key: AgentContextMaxFiles, kind: number, Default: "400", min: 1, max: 20000, Description: "Most agent-context files per lane"},
	{Key: AgentContextOnSync, kind: boolean, Default: "on", Description: "Refresh unmodified agent-context copies from the main checkout whenever a lane syncs"},
}

func find(key string) (Definition, bool) {
	for _, d := range Definitions {
		if d.Key == key {
			return d, true
		}
	}
	return Definition{}, false
}

// Entry is one resolved setting and where it came from.
type Entry struct {
	Key         string   `json:"key"`
	Values      []string `json:"values,omitempty"`
	Source      string   `json:"source"`
	Origin      string   `json:"origin,omitempty"`
	Description string   `json:"description"`
}

// Resolved holds the effective settings.
type Resolved struct {
	entries map[string]Entry
	// File is the settings file that was read, or "" when there was none.
	File string
	// Candidate is where a settings file would be looked for or created.
	Candidate string
	// ProjectFile is the per-repository settings file that was applied, or "".
	ProjectFile string
}

// Inputs are the sources, injectable so every precedence rule can be tested
// without touching the real environment or user directory.
type Inputs struct {
	Explicit  string
	Environ   func(string) (string, bool)
	Flags     map[string][]string
	ConfigDir func() (string, error)
	ReadFile  func(string) ([]byte, error)
	GOOS      string
	// AllowMissing accepts a named settings file that does not exist yet, for
	// `settings --init`, which creates it.
	AllowMissing bool
}

// DefaultInputs reads the real process environment and filesystem.
func DefaultInputs(explicit string, flags map[string][]string) Inputs {
	return Inputs{Explicit: explicit, Environ: os.LookupEnv, Flags: flags, ConfigDir: os.UserConfigDir, ReadFile: os.ReadFile, GOOS: runtime.GOOS}
}

// DefaultPath is the per-user settings file location: %APPDATA%\Workcopy,
// ~/Library/Application Support/Workcopy or $XDG_CONFIG_HOME/workcopy.
func DefaultPath(configDir func() (string, error), goos string) (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	name := "workcopy"
	if goos == "windows" || goos == "darwin" {
		name = "Workcopy"
	}
	return filepath.Join(dir, name, "workcopy.env"), nil
}

// named keeps the original failure code (a foreign-path error stays one) and
// adds the setting that carried the bad value.
func named(key string, err error) error {
	var failure *model.Failure
	if errors.As(err, &failure) {
		return &model.Failure{Exit: failure.Exit, Code: failure.Code, Message: key + ": " + failure.Message}
	}
	return invalid(key, err.Error())
}

func invalid(key, why string) error {
	return model.Fail(2, "invalid-setting", fmt.Sprintf("%s: %s", key, why))
}

// Load resolves all settings.
func Load(in Inputs) (*Resolved, error) {
	r := &Resolved{entries: map[string]Entry{}}
	file, fromFlagOrEnv := in.Explicit, in.Explicit != ""
	if file == "" {
		if value, ok := in.Environ(FileEnvironment); ok && value != "" {
			file, fromFlagOrEnv = value, true
		}
	}
	if file == "" {
		if candidate, err := DefaultPath(in.ConfigDir, in.GOOS); err == nil {
			r.Candidate = candidate
			file = candidate
		}
	} else {
		r.Candidate = file
	}
	var fileValues map[string][]string
	if file != "" {
		native, err := fsops.NormalizeInput(nil, file)
		if err != nil {
			return nil, err
		}
		file = native
		if r.Candidate != "" {
			r.Candidate = native
		}
		body, err := in.ReadFile(file)
		switch {
		case err == nil:
			fileValues, err = ParseEnvFile(body)
			if err != nil {
				return nil, model.Fail(2, "invalid-settings-file", fmt.Sprintf("%s: %v", file, err))
			}
			r.File = file
		case os.IsNotExist(err) && (!fromFlagOrEnv || in.AllowMissing):
			// No per-user file is the normal first-run state.
		case os.IsNotExist(err):
			return nil, model.Fail(2, "settings-file-not-found", fmt.Sprintf("The settings file %s does not exist; create it with `workcopy settings --init --settings %s`", file, file))
		default:
			return nil, fmt.Errorf("read settings file %s: %w", file, err)
		}
	}
	for _, d := range Definitions {
		entry := Entry{Key: d.Key, Description: d.Description, Source: "default"}
		if d.Default != "" {
			entry.Values = []string{d.Default}
		}
		if values, ok := fileValues[d.Key]; ok {
			entry.Values, entry.Source, entry.Origin = values, "file", r.File
		}
		if value, ok := in.Environ(d.Key); ok && value != "" {
			entry.Values, entry.Source, entry.Origin = []string{value}, "environment", d.Key
		}
		if values, ok := in.Flags[d.Key]; ok && len(values) > 0 {
			entry.Values, entry.Source, entry.Origin = values, "flag", "--"+d.Flag
		}
		if d.kind == list {
			entry.Values = splitList(entry.Values, d.Key == Agents)
		}
		if err := validate(d, entry.Values); err != nil {
			return nil, err
		}
		entry.Values = nativePaths(d, entry.Values)
		r.entries[d.Key] = entry
	}
	return r, nil
}

// nativePaths rewrites the other platform's spelling of a path setting into this
// platform's, so every consumer sees one form. The spelling the user chose is
// remembered for the answers.
func nativePaths(d Definition, values []string) []string {
	if d.kind != path && d.Key != AllowShareTarget {
		return values
	}
	converted := make([]string, len(values))
	for i, value := range values {
		native, err := fsops.NormalizeInput(nil, value)
		if err != nil {
			native = value
		}
		converted[i] = native
	}
	return converted
}

// splitList separates list values at semicolons. Tool names also split at
// commas, which survive an unquoted shell command line where a semicolon does not.
func splitList(values []string, commas bool) []string {
	result := []string{}
	for _, value := range values {
		if commas {
			value = strings.ReplaceAll(value, ",", ";")
		}
		for _, part := range strings.Split(value, ";") {
			if part = strings.TrimSpace(part); part != "" {
				result = append(result, part)
			}
		}
	}
	return result
}

func parseBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

func validate(d Definition, values []string) error {
	if len(values) == 0 {
		return nil
	}
	if d.kind != list && len(values) > 1 {
		values = values[len(values)-1:]
	}
	for _, value := range values {
		switch d.kind {
		case boolean:
			if _, ok := parseBool(value); !ok {
				return invalid(d.Key, fmt.Sprintf("%q is not on/off, true/false, yes/no or 1/0", value))
			}
		case number:
			n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil || n < d.min || n > d.max {
				return invalid(d.Key, fmt.Sprintf("%q must be a whole number from %d to %d", value, d.min, d.max))
			}
		case duration:
			n, err := time.ParseDuration(strings.TrimSpace(value))
			if err != nil || int64(n) < d.min || int64(n) > d.max {
				return invalid(d.Key, fmt.Sprintf("%q must be a duration such as 90s, 15m or 2h between %v and %v", value, time.Duration(d.min), time.Duration(d.max)))
			}
		case text:
			if d.Key == PathStyle {
				if _, _, err := fsops.ParsePathStyle(value); err != nil {
					return invalid(d.Key, err.Error())
				}
			}
		case path:
			if _, _, err := fsops.ToNative(value); err != nil {
				return named(d.Key, err)
			}
		case list:
			switch d.Key {
			case AllowShareTarget:
				if _, _, err := fsops.ToNative(value); err != nil {
					return named(d.Key, err)
				}
			case Agents:
				if !agents.ValidSelector(value) {
					return invalid(d.Key, fmt.Sprintf("%q is not auto, all, generic or one of: %s", value, strings.Join(agents.Keys(), ", ")))
				}
			case AgentContextConfig, AgentContextSecrets:
				if err := checkGlob(value); err != nil {
					return invalid(d.Key, err.Error())
				}
				if err := checkOptIn(d.Key, value); err != nil {
					return invalid(d.Key, err.Error())
				}
			default:
				if err := checkGlob(value); err != nil {
					return invalid(d.Key, err.Error())
				}
			}
		}
	}
	return nil
}

// checkGlob accepts a repository-relative pattern and nothing that could
// address a path outside the repository.
func checkGlob(pattern string) error {
	trimmed := strings.Trim(pattern, "/")
	if trimmed == "" || trimmed == "." || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "\\") || len(pattern) > 256 {
		return fmt.Errorf("%q must be a relative pattern using / separators", pattern)
	}
	for _, part := range strings.Split(trimmed, "/") {
		if part == ".." || part == "" {
			return fmt.Errorf("%q must not contain empty or .. segments", pattern)
		}
	}
	return nil
}

// checkOptIn narrows the patterns that may name configuration files. A pattern
// must begin with a literal directory or file so it cannot sweep the whole
// repository, and a secrets entry must be one exact file.
func checkOptIn(key, pattern string) error {
	first := strings.Split(strings.Trim(pattern, "/"), "/")[0]
	if strings.ContainsAny(first, "*?[") {
		return fmt.Errorf("%q must begin with a literal directory or file name, not a wildcard", pattern)
	}
	if key == AgentContextSecrets && strings.ContainsAny(pattern, "*?[") {
		return fmt.Errorf("%q must name one exact file; wildcards are not allowed for files that may hold credentials", pattern)
	}
	return nil
}

// ParseEnvFile reads dotenv text. Keys that do not start with WORKCOPY_ are
// ignored so the file can be shared with other tools; an unknown WORKCOPY_ key
// is an error, because a typo would otherwise silently do nothing.
func ParseEnvFile(data []byte) (map[string][]string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	result := map[string][]string{}
	for n, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", n+1)
		}
		key = strings.TrimSpace(key)
		parsed, err := parseValue(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", n+1, err)
		}
		if !strings.HasPrefix(key, "WORKCOPY_") {
			continue
		}
		definition, known := find(key)
		if !known && key != FileEnvironment {
			return nil, fmt.Errorf("line %d: unknown setting %s (run `workcopy settings` for the list)", n+1, key)
		}
		if key == FileEnvironment {
			return nil, fmt.Errorf("line %d: %s cannot be set inside a settings file", n+1, key)
		}
		if definition.kind == list {
			result[key] = append(result[key], parsed)
		} else {
			result[key] = []string{parsed}
		}
	}
	return result, nil
}

func parseValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	switch value[0] {
	case '\'':
		end := strings.IndexByte(value[1:], '\'')
		if end < 0 {
			return "", fmt.Errorf("unterminated single quote")
		}
		return value[1 : 1+end], nil
	case '"':
		var out strings.Builder
		for i := 1; i < len(value); i++ {
			switch c := value[i]; {
			case c == '\\' && i+1 < len(value):
				i++
				switch value[i] {
				case 'n':
					out.WriteByte('\n')
				case 't':
					out.WriteByte('\t')
				default:
					out.WriteByte(value[i])
				}
			case c == '"':
				return out.String(), nil
			default:
				out.WriteByte(c)
			}
		}
		return "", fmt.Errorf("unterminated double quote")
	}
	if hash := strings.Index(value, " #"); hash >= 0 {
		value = value[:hash]
	}
	return strings.TrimSpace(value), nil
}

// Entry returns the resolved entry for a key.
func (r *Resolved) Entry(key string) Entry { return r.entries[key] }

// Entries returns every setting in display order.
func (r *Resolved) Entries() []Entry {
	result := make([]Entry, 0, len(Definitions))
	for _, d := range Definitions {
		result = append(result, r.entries[d.Key])
	}
	return result
}

// String returns the last value of a setting, or "".
func (r *Resolved) String(key string) string {
	values := r.entries[key].Values
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

// List returns every value of a list setting.
func (r *Resolved) List(key string) []string { return append([]string(nil), r.entries[key].Values...) }

// Int returns a numeric setting; 0 when unset with no default.
func (r *Resolved) Int(key string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(r.String(key)))
	return n
}

// Duration returns a duration setting.
func (r *Resolved) Duration(key string) time.Duration {
	d, _ := time.ParseDuration(strings.TrimSpace(r.String(key)))
	return d
}

// Bool returns a boolean setting.
func (r *Resolved) Bool(key string) bool {
	value, _ := parseBool(r.String(key))
	return value
}

// Overridden reports whether a setting came from anywhere but its default.
func (r *Resolved) Overridden(key string) bool { return r.entries[key].Source != "default" }

// ExportProcessEnvironment makes settings that lower layers read straight from
// the environment (the Git program, the I/O worker count) visible to them when
// they were set in the file or a flag.
func (r *Resolved) ExportProcessEnvironment(environ func(string) (string, bool), set func(string, string) error) error {
	// The file-size limit lives in the model package, which cannot read settings.
	if mib := int64(r.Int(MaxFileMiB)); mib >= 64 {
		model.SetMaxFileBytes(mib << 20)
	}
	for _, key := range []string{Git, IOWorkers, WSLMountRoot} {
		if _, already := environ(key); already {
			continue
		}
		if value := r.String(key); value != "" {
			if err := set(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// Template is the commented starting file written by `workcopy settings --init`.
func Template() string {
	var out strings.Builder
	out.WriteString("# Workcopy settings. KEY=VALUE per line; # starts a comment.\n")
	out.WriteString("# Precedence: command-line flag > environment variable > this file > built-in default.\n")
	out.WriteString("# Run `workcopy settings` to see every effective value and where it came from.\n\n")
	for _, d := range Definitions {
		fmt.Fprintf(&out, "# %s", d.Description)
		if d.Default != "" {
			fmt.Fprintf(&out, " (default: %s)", d.Default)
		}
		fmt.Fprintf(&out, "\n# %s=\n\n", d.Key)
	}
	return out.String()
}

// Builtin returns the defaults with no file, environment or flags, for callers
// (libraries, tests) that were handed no settings.
func Builtin() *Resolved {
	r, err := Load(Inputs{
		Environ:   func(string) (string, bool) { return "", false },
		ConfigDir: func() (string, error) { return "", fmt.Errorf("no configuration directory") },
		ReadFile:  func(string) ([]byte, error) { return nil, os.ErrNotExist },
	})
	if err != nil {
		panic("built-in settings are invalid: " + err.Error())
	}
	return r
}

// ProjectFileName is the per-repository settings file, kept in the repository's
// Git directory. Git never tracks that directory, so the file cannot be committed
// or shipped by a clone, and it is not part of any lane.
const ProjectFileName = "workcopy.env"

// ProjectPath is where a repository's settings file lives.
func ProjectPath(commonGitDir string) string { return filepath.Join(commonGitDir, ProjectFileName) }

// projectKeys are the settings that make sense per repository and are safe to
// take from a file inside the repository's Git directory. The Git program, the
// share-target allow-list and the MCP limits stay with the user's own settings
// file: a program to run and a directory to link are not a project's to choose.
var projectKeys = map[string]bool{
	Root: true, Config: true, MaxFileMiB: true, IOWorkers: true, Agents: true,
	AgentContext: true, AgentContextDetect: true, AgentContextInclude: true, AgentContextExclude: true,
	AgentContextConfig: true, AgentContextSecrets: true, AgentContextMaxFileKiB: true,
	AgentContextMaxKiB: true, AgentContextMaxFiles: true, AgentContextOnSync: true,
}

// ProjectKey reports whether a setting may be set per repository.
func ProjectKey(key string) bool { return projectKeys[key] }

// WithProject layers a repository's settings file between the user's file and the
// environment: flag > environment > project file > user file > default. A key the
// project file sets replaces the lower layers' value for that key. The receiver
// is not modified.
func (r *Resolved) WithProject(file string, body []byte) (*Resolved, error) {
	values, err := ParseEnvFile(body)
	if err != nil {
		return nil, model.Fail(2, "invalid-settings-file", fmt.Sprintf("%s: %v", file, err))
	}
	merged := &Resolved{entries: make(map[string]Entry, len(r.entries)), File: r.File, Candidate: r.Candidate, ProjectFile: file}
	for key, entry := range r.entries {
		merged.entries[key] = entry
	}
	for _, d := range Definitions {
		given, ok := values[d.Key]
		if !ok {
			continue
		}
		if !projectKeys[d.Key] {
			return nil, model.Fail(2, "invalid-settings-file", fmt.Sprintf("%s: %s cannot be set per repository; put it in the user settings file (`workcopy settings` shows where)", file, d.Key))
		}
		if d.kind == list {
			given = splitList(given, d.Key == Agents)
		}
		if err = validate(d, given); err != nil {
			return nil, model.Fail(2, "invalid-settings-file", fmt.Sprintf("%s: %v", file, err))
		}
		given = nativePaths(d, given)
		if d.kind == path {
			for _, value := range given {
				if value != "" && !filepath.IsAbs(value) {
					return nil, model.Fail(2, "invalid-settings-file", fmt.Sprintf("%s: %s must be an absolute path, not %q", file, d.Key, value))
				}
			}
		}
		current := merged.entries[d.Key]
		if current.Source == "flag" || current.Source == "environment" {
			continue
		}
		current.Values, current.Source, current.Origin = given, "project", file
		merged.entries[d.Key] = current
	}
	return merged, nil
}

// ProjectTemplate is the commented starting file written by
// `workcopy settings --init --project`.
func ProjectTemplate() string {
	var out strings.Builder
	out.WriteString("# Workcopy settings for this repository only. KEY=VALUE per line.\n")
	out.WriteString("# Precedence: flag > environment > this file > your user settings file > default.\n")
	out.WriteString("# This file lives in the repository's Git directory, so Git never tracks it.\n\n")
	for _, d := range Definitions {
		if !projectKeys[d.Key] {
			continue
		}
		fmt.Fprintf(&out, "# %s", d.Description)
		if d.Default != "" {
			fmt.Fprintf(&out, " (default: %s)", d.Default)
		}
		fmt.Fprintf(&out, "\n# %s=\n\n", d.Key)
	}
	return out.String()
}
