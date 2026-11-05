package agentctx

import (
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"workcopy/internal/agents"
	"workcopy/internal/fsops"
	"workcopy/internal/model"
	"workcopy/internal/project"
)

// Options controls one plan. The zero value copies nothing.
type Options struct {
	// Detect enables the built-in catalogue.
	Detect bool
	// AllAgents selects every tool in the catalogue. Otherwise Agents lists the
	// tool keys to serve; the tool-neutral files (AGENTS.md and kin) are always
	// included while Detect is on.
	AllAgents bool
	Agents    []string
	// Config lists repository-relative globs of tool configuration files the
	// owner opted in. They may be settings or MCP definitions but not
	// credentials. ConfigSecrets lists exact files (no wildcards) the owner
	// accepted may hold credentials; their content is not scanned. Both come
	// only from the owner's settings, never from the repository or an agent.
	Config, ConfigSecrets []string
	// Include adds repository-relative globs; Exclude removes matches last.
	Include, Exclude []string
	// Limits bound what a lane receives. A file or total over a limit is
	// skipped and reported, never truncated.
	MaxFileBytes, MaxTotalBytes int64
	MaxFiles                    int
	// ExecBits keeps the execute bit of scripts (only on filesystems that store it).
	ExecBits bool
	// Tracked reports paths the lane already receives from Git; they are skipped.
	Tracked func(rel string) bool
}

// File is one file to place in a lane.
type File struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Size   int64  `json:"size"`
	// Sensitive marks a file the owner accepted may contain credentials.
	Sensitive bool   `json:"sensitive,omitempty"`
	Mode      uint32 `json:"-"`
	Hash      string `json:"sha256"`
	Body      []byte `json:"-"`
}

// Skipped is a file that matched but was refused, with the reason.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Candidate is a directory that looks like agent context but is not in the
// catalogue and was not included. It is reported so the owner can decide.
type Candidate struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
	Hint  string `json:"hint"`
}

// Plan is what a lane would receive and what was left out.
type Plan struct {
	Files      []File      `json:"files"`
	Skipped    []Skipped   `json:"skipped,omitempty"`
	Candidates []Candidate `json:"candidates,omitempty"`
	Tools      []string    `json:"tools,omitempty"`
	// NotSelected names known tools whose files exist in the checkout but are
	// not served to this lane, so a missing rule file can be explained.
	NotSelected []string `json:"notSelected,omitempty"`
	TotalBytes  int64    `json:"totalBytes"`
}

type source struct {
	glob, tool string
	class      Class
}

type hit struct {
	tool  string
	class Class
}

// ConfigSource and SecretSource name the owner's lists in reports.
const (
	ConfigSource = "your config list"
	SecretSource = "your secrets list"
)

const (
	maxWalkEntries   = 20000
	maxSkippedListed = 200
)

func hasWildcard(s string) bool { return strings.ContainsAny(s, "*?[") }

// walkRoot returns the longest wildcard-free directory prefix of a glob.
func walkRoot(glob string) string {
	parts := strings.Split(strings.TrimSuffix(glob, "/"), "/")
	keep := []string{}
	for _, part := range parts {
		if hasWildcard(part) {
			break
		}
		keep = append(keep, part)
	}
	return strings.Join(keep, "/")
}

// Build inspects the checkout rooted at root and returns the plan.
func Build(root *fsops.Root, opts Options) (Plan, error) {
	var plan Plan
	sources := []source{}
	selected := map[string]bool{agents.GenericKey: true}
	for _, key := range opts.Agents {
		selected[strings.ToLower(strings.TrimSpace(key))] = true
	}
	var unselected []agents.Tool
	if opts.Detect {
		for _, tool := range agents.Catalogue {
			if !opts.AllAgents && !selected[tool.Key] {
				unselected = append(unselected, tool)
				continue
			}
			for _, glob := range tool.Globs {
				sources = append(sources, source{glob, tool.Name, Instructions})
			}
		}
	}
	for _, glob := range opts.Include {
		sources = append(sources, source{strings.TrimSuffix(glob, "/"), "your include list", Instructions})
	}
	for _, glob := range opts.Config {
		sources = append(sources, source{strings.TrimSuffix(glob, "/"), ConfigSource, Config})
	}
	for _, name := range opts.ConfigSecrets {
		if !hasWildcard(name) {
			sources = append(sources, source{strings.TrimSuffix(name, "/"), SecretSource, Secrets})
		}
	}
	// literal names (no wildcard) may have any extension; everything else must
	// be an instruction or script format.
	literal := map[string]bool{}
	for _, s := range sources {
		if !hasWildcard(s.glob) {
			literal[strings.ToLower(path.Base(s.glob))] = true
		}
	}
	found := map[string]hit{}
	seenRoots := map[string]bool{}
	walked := 0
	for _, s := range sources {
		start := walkRoot(s.glob)
		if start == "" || seenRoots[start] {
			continue
		}
		seenRoots[start] = true
		err := root.WalkFrom(start, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if name != start && deniedDirectories[strings.ToLower(path.Base(name))] {
					return fs.SkipDir
				}
				return nil
			}
			walked++
			if walked > maxWalkEntries {
				return fs.SkipAll
			}
			if entry.Type()&os.ModeSymlink != 0 {
				plan.skip(name, "symbolic link")
				return nil
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			// The most trusted class that matches decides how the file is judged.
			for _, candidate := range sources {
				if !project.Match(candidate.glob, name) {
					continue
				}
				if previous, dup := found[name]; !dup || candidate.class > previous.class {
					found[name] = hit{candidate.tool, candidate.class}
				}
			}
			return nil
		})
		if err != nil {
			return plan, err
		}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	toolSeen := map[string]bool{}
	for _, name := range names {
		if err := fsops.ValidateSourcePath(name); err != nil {
			plan.skip(name, "not a portable lane path")
			continue
		}
		switch {
		case matchesAny(opts.Exclude, name):
			plan.skip(name, "excluded by your exclude list")
			continue
		case opts.Tracked != nil && opts.Tracked(name):
			continue
		}
		class := found[name].class
		if reason := DenyReasonFor(name, class); reason != "" {
			plan.skip(name, reason)
			continue
		}
		base := path.Base(name)
		allowed := ExtensionAllowed(base)
		if class != Instructions {
			allowed = ConfigExtensionAllowed(base)
		}
		if !allowed && !literal[strings.ToLower(base)] {
			plan.skip(name, "not an instruction, script or settings file type")
			continue
		}
		info, err := root.Lstat(name)
		if err != nil {
			return plan, err
		}
		if info.Size() > opts.MaxFileBytes {
			plan.skip(name, "larger than "+kib(opts.MaxFileBytes)+" KiB")
			continue
		}
		if len(plan.Files) >= opts.MaxFiles {
			plan.skip(name, "file-count limit reached")
			continue
		}
		if plan.TotalBytes+info.Size() > opts.MaxTotalBytes {
			plan.skip(name, "total-size limit reached")
			continue
		}
		body, err := root.ReadRegular(name, info, opts.MaxFileBytes)
		if err != nil {
			return plan, err
		}
		// A file the owner named as holding credentials is not scanned for them,
		// but it must still be text.
		if reason := ContentReason(body); reason != "" && (class != Secrets || reason == NotText) {
			plan.skip(name, reason)
			continue
		}
		mode := uint32(0644)
		if opts.ExecBits && info.Mode().Perm()&0111 != 0 {
			mode = 0755
		}
		plan.Files = append(plan.Files, File{Path: name, Source: found[name].tool, Size: info.Size(), Sensitive: class == Secrets, Mode: mode, Hash: model.Hash(body), Body: body})
		plan.TotalBytes += info.Size()
		toolSeen[found[name].tool] = true
	}
	for _, tool := range append(agents.ToolNames(), "your include list", ConfigSource, SecretSource) {
		if toolSeen[tool] {
			plan.Tools = append(plan.Tools, tool)
		}
	}
	covered := map[string]bool{}
	for start := range seenRoots {
		covered[strings.SplitN(start, "/", 2)[0]] = true
	}
	// Known tools that were not selected are not "unrecognised" either.
	for _, tool := range agents.Catalogue {
		for _, glob := range tool.Globs {
			if start := walkRoot(glob); start != "" {
				covered[strings.SplitN(start, "/", 2)[0]] = true
			}
		}
	}
	plan.NotSelected = presentTools(root, unselected)
	plan.Candidates = findCandidates(root, covered, opts)
	return plan, nil
}

func kib(n int64) string { return strconv.FormatInt(n/1024, 10) }

func matchesAny(globs []string, name string) bool {
	for _, glob := range globs {
		if project.Match(strings.TrimSuffix(glob, "/"), name) {
			return true
		}
	}
	return false
}

func (p *Plan) skip(name, reason string) {
	if len(p.Skipped) < maxSkippedListed {
		p.Skipped = append(p.Skipped, Skipped{name, reason})
	}
}

var ignoredTopLevel = map[string]bool{
	".git": true, ".github": true, ".vs": true, ".vscode": true, ".idea": true, ".gradle": true, ".mvn": true, ".nuget": true,
	".cache": true, ".husky": true, ".devcontainer": true, ".circleci": true, ".gitlab": true, ".docker": true, ".terraform": true,
	".next": true, ".nuxt": true, ".venv": true, ".yarn": true, ".pnpm-store": true, ".config": true, ".local": true, ".npm": true,
	".nvm": true, ".cargo": true, ".rustup": true, ".dotnet": true, ".aws": true, ".azure": true, ".ssh": true, ".gnupg": true,
	".workcopy": true, ".svn": true, ".hg": true, ".tox": true, ".pytest_cache": true, ".mypy_cache": true, ".ruff_cache": true,
}

var instructionHints = []string{"rules", "prompts", "instructions", "steering", "skills", "agents", "commands", "memory", "guidelines", "workflows", "hooks", "specs", "microagents", "modes"}

// findCandidates reports top-level dot-directories that hold instruction-like
// text but are not in the catalogue, so that a new tool is surfaced rather than
// silently ignored or silently copied.
func findCandidates(root *fsops.Root, covered map[string]bool, opts Options) []Candidate {
	var result []Candidate
	entries := []fs.DirEntry{}
	_ = root.Walk(func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if name == "." {
			return nil
		}
		if entry.IsDir() && !strings.Contains(name, "/") {
			entries = append(entries, entry)
		}
		if entry.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".") || ignoredTopLevel[strings.ToLower(name)] || covered[name] || matchesAny(opts.Exclude, name+"/x") {
			continue
		}
		candidate := Candidate{Path: name}
		hints := map[string]bool{}
		count := 0
		_ = root.WalkFrom(name, func(rel string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			count++
			if count > 400 || strings.Count(rel, "/") > 4 {
				return fs.SkipDir
			}
			if e.IsDir() {
				if deniedDirectories[strings.ToLower(e.Name())] {
					return fs.SkipDir
				}
				for _, hint := range instructionHints {
					if strings.Contains(strings.ToLower(e.Name()), hint) {
						hints[hint] = true
					}
				}
				return nil
			}
			if !e.Type().IsRegular() || DenyReason(rel) != "" || !ExtensionAllowed(e.Name()) {
				return nil
			}
			if opts.Tracked != nil && opts.Tracked(rel) {
				return nil
			}
			info, infoErr := e.Info()
			if infoErr != nil || info.Size() > opts.MaxFileBytes {
				return nil
			}
			candidate.Files++
			candidate.Bytes += info.Size()
			return nil
		})
		if candidate.Files == 0 || len(hints) == 0 {
			continue
		}
		list := make([]string, 0, len(hints))
		for hint := range hints {
			list = append(list, hint)
		}
		sort.Strings(list)
		candidate.Hint = "contains " + strings.Join(list, ", ")
		result = append(result, candidate)
		if len(result) >= 20 {
			break
		}
	}
	return result
}

// presentTools names the tools whose files exist in the checkout. It checks
// only the first existing entry per tool, so it costs a few stat calls.
func presentTools(root *fsops.Root, tools []agents.Tool) []string {
	var present []string
	for _, tool := range tools {
		for _, glob := range tool.Globs {
			start := walkRoot(glob)
			if start == "" {
				// A literal file such as CLAUDE.md is its own start.
				start = glob
			}
			if _, err := root.Lstat(start); err == nil {
				present = append(present, tool.Key)
				break
			}
		}
	}
	sort.Strings(present)
	return present
}
