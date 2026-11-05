package workspace

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"workcopy/internal/agentctx"
	"workcopy/internal/agents"
	"workcopy/internal/fsops"
	"workcopy/internal/model"
	"workcopy/internal/settings"
	"workcopy/internal/state"
)

// Agent context is the set of untracked instruction files (rules, steering,
// prompts, skills, notes) that guide an agent working in a lane. They live in
// the main checkout, usually untracked, so a Git-free lane would not have them.
// Each lane receives a private copy chosen by agentctx, remembered in its
// manifest, kept out of status, reviews and promotion, discarded by destroy,
// and refreshed from the main checkout on request and on sync.

// ContextReport is the machine-readable result of the agent-context commands.
type ContextReport struct {
	Enabled bool `json:"enabled"`
	Detect  bool `json:"detect"`
	// Agents are the tool keys this lane serves ("all" for every tool), and
	// AgentsSource says why: a request, the setting, the detected MCP client
	// or the lane's own record.
	Agents       []string `json:"agents,omitempty"`
	AgentsSource string   `json:"agentsSource,omitempty"`
	Client       string   `json:"client,omitempty"`
	// NotSelected names known tools whose files exist but are not served.
	NotSelected  []string             `json:"notSelected,omitempty"`
	Tools        []string             `json:"tools,omitempty"`
	Included     []ContextEntry       `json:"included,omitempty"`
	Skipped      []agentctx.Skipped   `json:"skipped,omitempty"`
	Candidates   []agentctx.Candidate `json:"candidates,omitempty"`
	Added        []string             `json:"added,omitempty"`
	Updated      []string             `json:"updated,omitempty"`
	Removed      []string             `json:"removed,omitempty"`
	KeptModified []string             `json:"keptModified,omitempty"`
	KeptMissing  []string             `json:"keptMissing,omitempty"`
	Obstructed   []string             `json:"obstructed,omitempty"`
	TotalBytes   int64                `json:"totalBytes"`
	SettingsFile string               `json:"settingsFile,omitempty"`
	Guidance     string               `json:"guidance"`
}

// ContextEntry is one file that is or would be copied.
type ContextEntry struct {
	Path      string `json:"path"`
	Source    string `json:"source"`
	Size      int64  `json:"size"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

type contextPolicy struct {
	Enabled, Detect, RefreshOnSync bool
	Include, Exclude               []string
	// Config and Secrets are the owner's opt-in tool configuration files.
	Config, Secrets []string
	// Agents is the configured selection; AgentsFlagged is true when it came
	// from a command-line flag, which counts as an explicit request.
	Agents            []string
	AgentsFlagged     bool
	MaxFile, MaxTotal int64
	MaxFiles          int
}

func (e *Engine) contextPolicy() contextPolicy {
	s := e.Settings
	if s == nil {
		s = settings.Builtin()
	}
	return contextPolicy{
		Enabled: s.Bool(settings.AgentContext), Detect: s.Bool(settings.AgentContextDetect), RefreshOnSync: s.Bool(settings.AgentContextOnSync),
		Include: s.List(settings.AgentContextInclude), Exclude: s.List(settings.AgentContextExclude),
		Config: s.List(settings.AgentContextConfig), Secrets: s.List(settings.AgentContextSecrets),
		Agents: s.List(settings.Agents), AgentsFlagged: s.Entry(settings.Agents).Source == "flag",
		MaxFile: int64(s.Int(settings.AgentContextMaxFileKiB)) * 1024, MaxTotal: int64(s.Int(settings.AgentContextMaxKiB)) * 1024, MaxFiles: s.Int(settings.AgentContextMaxFiles),
	}
}

func (e *Engine) settingsFile() string {
	if e.Settings == nil {
		return ""
	}
	if e.Settings.File != "" {
		return e.Settings.File
	}
	return e.Settings.Candidate
}

// contextGuidance tells a person or an agent how to change what is copied.
func (e *Engine) contextGuidance() string {
	where := e.settingsFile()
	if where == "" {
		where = "the Workcopy settings file (workcopy settings --init creates it)"
	}
	return "Only instruction text is copied: rules, steering, prompts, skills and notes from the agent tools a lane serves, never credentials, media, archives, history or caches. " +
		"To change it once for every lane, edit " + where + ": " + settings.Agents + "=kiro;codex chooses the tools (auto lets the MCP client decide), " +
		settings.AgentContextInclude + "=glob;glob adds repository-relative paths (listed under candidates are directories that look like agent context but are not recognised), " +
		settings.AgentContextExclude + "=glob removes paths, " + settings.AgentContextConfig + "=path;path opts in tool configuration such as an MCP definition (credentials stay refused), " +
		settings.AgentContextSecrets + "=path names exact files you accept may hold credentials, " + settings.AgentContext + "=off disables the feature. Then run agent-context --refresh in a lane, or create a new lane."
}

func (l *lane) contextSet() map[string]model.ContextFile {
	set := make(map[string]model.ContextFile, len(l.snapshot.Manifest.AgentContext))
	for _, file := range l.snapshot.Manifest.AgentContext {
		set[file.Path] = file
	}
	return set
}

// contextPlan builds the plan for this engine's repository. lane may be nil
// (a repository-level preview), in which case only the committed tree of HEAD
// decides what counts as tracked.
func (e *Engine) contextPlan(ctx context.Context, opened *lane, selection agents.Resolution) (agentctx.Plan, contextPolicy, error) {
	policy := e.contextPolicy()
	if !policy.Enabled {
		return agentctx.Plan{}, policy, nil
	}
	var tree map[string]bool
	trackedIn := func(rel string) bool {
		if opened != nil {
			if _, ok := opened.snapshot.Baseline.Files[rel]; ok {
				return true
			}
		}
		if tree == nil {
			tree = map[string]bool{}
			commit := ""
			if opened != nil {
				commit = opened.snapshot.Manifest.SyncedCommit
			} else if _, head, err := e.Repository.Resolve(ctx, ""); err == nil {
				commit = head
			}
			if commit != "" {
				if entries, err := e.Repository.Tree(ctx, commit); err == nil {
					for name := range entries {
						tree[name] = true
					}
				}
			}
		}
		return tree[rel]
	}
	root, err := fsops.OpenRoot(e.Repository.Root)
	if err != nil {
		return agentctx.Plan{}, policy, err
	}
	defer root.Close()
	execBits := opened != nil && opened.snapshot.Manifest.RespectExecutableBits
	plan, err := agentctx.Build(root, agentctx.Options{
		Detect: policy.Detect, AllAgents: selection.All, Agents: selection.Keys, Include: policy.Include, Exclude: policy.Exclude,
		Config: policy.Config, ConfigSecrets: policy.Secrets,
		MaxFileBytes: policy.MaxFile, MaxTotalBytes: policy.MaxTotal, MaxFiles: policy.MaxFiles,
		ExecBits: execBits, Tracked: trackedIn,
	})
	return plan, policy, err
}

func (e *Engine) baseContextReport(ctx context.Context, plan agentctx.Plan, policy contextPolicy, selection agents.Resolution) *ContextReport {
	report := &ContextReport{Enabled: policy.Enabled, Detect: policy.Detect, Agents: recordSelection(selection), AgentsSource: selection.Source, Client: clientName(ctx), NotSelected: plan.NotSelected, Tools: plan.Tools, Skipped: plan.Skipped, Candidates: plan.Candidates, TotalBytes: plan.TotalBytes, SettingsFile: e.settingsFile(), Guidance: e.contextGuidance()}
	for _, file := range plan.Files {
		report.Included = append(report.Included, ContextEntry{Path: file.Path, Source: file.Source, Size: file.Size, Sensitive: file.Sensitive})
	}
	return report
}

type contextEdit struct {
	path          string
	before, after model.FileVersion
}

// refreshAgentContext brings a lane's copies in line with the main checkout.
// The caller holds the lane lock (unless dryRun). It never overwrites or
// removes a copy that was edited in the lane, and never replaces a file it did
// not put there.
func (e *Engine) refreshAgentContext(ctx context.Context, opened *lane, dryRun bool, requested []string) (*ContextReport, error) {
	explicit := len(requested) > 0 || e.contextPolicy().AgentsFlagged
	selection := e.selectAgents(ctx, opened, requested)
	plan, policy, err := e.contextPlan(ctx, opened, selection)
	if err != nil {
		return nil, err
	}
	report := e.baseContextReport(ctx, plan, policy, selection)
	if !policy.Enabled {
		return report, nil
	}
	inventory := opened.contextSet()
	desired := make(map[string]agentctx.File, len(plan.Files))
	for _, file := range plan.Files {
		desired[file.Path] = file
	}
	readLane := func(name string) (model.FileVersion, error) {
		version, _, readErr := state.ReadVersion(opened.root, name, model.FileVersion{}, opened.snapshot.Manifest.RespectExecutableBits)
		return version, readErr
	}
	next := make(map[string]model.ContextFile, len(inventory))
	for path, file := range inventory {
		next[path] = file
	}
	edits := []contextEdit{}
	bodies := map[string][]byte{}
	names := make([]string, 0, len(desired))
	for name := range desired {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		file := desired[name]
		current, readErr := readLane(name)
		if readErr != nil {
			return nil, readErr
		}
		recorded, known := inventory[name]
		wanted := model.Version(model.Regular, file.Mode, file.Body)
		switch {
		case !known && current.Exists():
			if current.Hash == file.Hash {
				next[name] = model.ContextFile{Path: name, Hash: file.Hash, Size: file.Size, Source: file.Source, Sensitive: file.Sensitive}
			} else {
				report.Obstructed = append(report.Obstructed, name)
			}
		case !known:
			edits = append(edits, contextEdit{name, model.FileVersion{Kind: model.Missing}, wanted})
			bodies[name] = file.Body
			next[name] = model.ContextFile{Path: name, Hash: file.Hash, Size: file.Size, Source: file.Source, Sensitive: file.Sensitive}
			report.Added = append(report.Added, name)
		case !current.Exists():
			report.KeptMissing = append(report.KeptMissing, name)
		case current.Hash != recorded.Hash:
			report.KeptModified = append(report.KeptModified, name)
		case file.Hash != recorded.Hash:
			edits = append(edits, contextEdit{name, current, wanted})
			bodies[name] = file.Body
			next[name] = model.ContextFile{Path: name, Hash: file.Hash, Size: file.Size, Source: file.Source, Sensitive: file.Sensitive}
			report.Updated = append(report.Updated, name)
		}
	}
	// A copy whose source is gone (or now excluded) is removed only if untouched.
	gone := []string{}
	for name := range inventory {
		if _, still := desired[name]; !still {
			gone = append(gone, name)
		}
	}
	sort.Strings(gone)
	for _, name := range gone {
		current, readErr := readLane(name)
		if readErr != nil {
			return nil, readErr
		}
		switch {
		case !current.Exists():
			delete(next, name)
		case current.Hash != inventory[name].Hash:
			report.KeptModified = append(report.KeptModified, name)
		default:
			edits = append(edits, contextEdit{name, current, model.FileVersion{Kind: model.Missing}})
			delete(next, name)
			report.Removed = append(report.Removed, name)
		}
	}
	recorded := recordSelection(selection)
	// A lane records its tool selection when it is created; an explicit request
	// replaces the record. A lane that predates the selection keeps none.
	changedSelection := explicit && !slices.Equal(recorded, opened.snapshot.Manifest.ContextAgents)
	changedInventory := changedSelection || len(next) != len(inventory)
	for path, file := range next {
		if previous, ok := inventory[path]; !ok || previous != file {
			changedInventory = true
		}
	}
	if dryRun || (len(edits) == 0 && !changedInventory) {
		return report, nil
	}
	after := newGeneration(opened.snapshot)
	if changedSelection {
		after.Manifest.ContextAgents = recorded
	}
	after.Manifest.AgentContext = after.Manifest.AgentContext[:0:0]
	for _, file := range next {
		after.Manifest.AgentContext = append(after.Manifest.AgentContext, file)
	}
	sort.Slice(after.Manifest.AgentContext, func(i, j int) bool { return after.Manifest.AgentContext[i].Path < after.Manifest.AgentContext[j].Path })
	tx, err := opened.store.Begin(after)
	if err != nil {
		return nil, err
	}
	for _, edit := range edits {
		if edit.after.Exists() {
			if _, err = opened.store.PutBlob(bodies[edit.path]); err != nil {
				return nil, err
			}
		}
		if err = tx.AddStaged(edit.path, edit.before, edit.after, false); err != nil {
			return nil, err
		}
	}
	if err = tx.Prepare(); err != nil {
		return nil, err
	}
	if err = tx.Apply(); err != nil {
		return nil, err
	}
	opened.snapshot = after
	return report, nil
}

// AgentContext previews the agent-context plan for the repository (name == "")
// or for a lane, and with refresh applies it to the lane.
func (e *Engine) AgentContext(ctx context.Context, name string, refresh, dryRun bool) (Report, error) {
	return e.AgentContextFor(ctx, name, refresh, dryRun, nil)
}

// AgentContextFor is AgentContext with an explicit tool selection, for example
// ["kiro", "codex"] or ["all"]. On a refresh it replaces what the lane recorded.
func (e *Engine) AgentContextFor(ctx context.Context, name string, refresh, dryRun bool, agents []string) (Report, error) {
	report := Report{Command: "agent-context"}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := validateAgents(agents); err != nil {
		return report, err
	}
	if name == "" {
		if refresh {
			return report, model.Fail(2, "lane-required", "Refreshing needs a lane name; run agent-context without --refresh to preview the repository")
		}
		selection := e.selectAgents(ctx, nil, agents)
		plan, policy, err := e.contextPlan(ctx, nil, selection)
		if err != nil {
			return report, err
		}
		report.AgentContext = e.baseContextReport(ctx, plan, policy, selection)
		report.Message = contextSummary(report.AgentContext, "A new lane would receive")
		return report, nil
	}
	opened, err := e.openLane(name)
	if err != nil {
		report.Workspace = e.missingView(name, err)
		return report, err
	}
	defer opened.close()
	apply := refresh && !dryRun
	if apply {
		lock, lockErr := e.reloadLocked(opened)
		if lockErr != nil {
			return report, lockErr
		}
		defer lock.Close()
	}
	result, err := e.refreshAgentContext(ctx, opened, !apply, agents)
	if err != nil {
		return report, err
	}
	report.Workspace = view(opened.snapshot)
	report.AgentContext = result
	switch {
	case !result.Enabled:
		report.Message = "Agent context is switched off (" + settings.AgentContext + "=off)."
	case refresh && !dryRun:
		report.Message = contextOutcome(result, "Agent context refreshed")
		if len(result.Added)+len(result.Updated)+len(result.Removed) == 0 {
			report.ExitCode = 4
			report.Message = contextOutcome(result, "Agent context already up to date")
		}
	default:
		report.Message = contextOutcome(result, "Agent context would change")
	}
	return report, nil
}

func contextSummary(r *ContextReport, lead string) string {
	if !r.Enabled {
		return "Agent context is switched off (" + settings.AgentContext + "=off)."
	}
	message := fmt.Sprintf("%s %d files (%d bytes) from %s.", lead, len(r.Included), r.TotalBytes, listOrNone(r.Tools))
	if len(r.Agents) > 0 {
		message += " Serving " + strings.Join(r.Agents, ", ") + " (" + r.AgentsSource + ")."
	}
	if len(r.NotSelected) > 0 {
		message += " Present but not served: " + strings.Join(r.NotSelected, ", ") + " (name them in " + settings.Agents + " or pass agents to include them)."
	}
	sensitive := 0
	for _, entry := range r.Included {
		if entry.Sensitive {
			sensitive++
		}
	}
	if sensitive > 0 {
		message += fmt.Sprintf(" %d of them may contain credentials, as you asked.", sensitive)
	}
	if len(r.Candidates) > 0 {
		names := []string{}
		for _, c := range r.Candidates {
			names = append(names, c.Path)
		}
		message += " Not copied but look like agent context: " + strings.Join(names, ", ") + "."
	}
	return message
}

func listOrNone(values []string) string {
	if len(values) == 0 {
		return "no known agent tools"
	}
	return strings.Join(values, ", ")
}

func contextOutcome(r *ContextReport, lead string) string {
	parts := []string{fmt.Sprintf("%s: %d added, %d updated, %d removed", lead, len(r.Added), len(r.Updated), len(r.Removed))}
	if len(r.KeptModified) > 0 {
		parts = append(parts, fmt.Sprintf("%d edited in the lane and left alone", len(r.KeptModified)))
	}
	if len(r.KeptMissing) > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted in the lane and not restored", len(r.KeptMissing)))
	}
	if len(r.Obstructed) > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked by a different local file", len(r.Obstructed)))
	}
	return strings.Join(parts, "; ") + "."
}

// copyInitialContext seeds a freshly created lane. Failure never fails the
// creation: the lane is valid without it and `agent-context --refresh` retries.
func (e *Engine) copyInitialContext(ctx context.Context, name string) (*ContextReport, string) {
	opened, err := e.openLane(name)
	if err != nil {
		return nil, "agent context was not copied: " + err.Error()
	}
	defer opened.close()
	lock, err := e.reloadLocked(opened)
	if err != nil {
		return nil, "agent context was not copied: " + err.Error()
	}
	defer lock.Close()
	result, err := e.refreshAgentContext(ctx, opened, false, nil)
	if err != nil {
		return nil, "agent context was not copied (run agent-context --refresh " + name + " to retry): " + err.Error()
	}
	return result, ""
}
