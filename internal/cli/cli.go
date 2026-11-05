package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
	"workcopy/internal/settings"
	"workcopy/internal/workspace"
)

var Version = "dev"

type operationError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}
type response struct {
	workspace.Report
	Error *operationError `json:"error,omitempty"`
}

// Run returns the exact process exit code also written to the JSON envelope.
// stdout contains one JSON object in machine mode; diagnostics use stderr.
func Run(ctx context.Context, input []string, stdout, stderr io.Writer) int {
	parsed, err := parse(input)
	jsonMode := false
	for _, value := range input {
		if value == "--json" {
			jsonMode = true
		}
	}
	if err != nil {
		return writeResult(stdout, stderr, jsonMode, workspace.Report{Command: parsed.command}, usage(err))
	}
	jsonMode = parsed.boolean("json")
	if parsed.command == "help" || parsed.boolean("help") {
		if jsonMode {
			return writeResult(stdout, stderr, true, workspace.Report{Command: "help", Message: helpText}, nil)
		}
		_, err = io.WriteString(stdout, helpText)
		if err != nil {
			return 5
		}
		return 0
	}
	if parsed.command == "version" {
		if len(parsed.positionals) != 0 {
			return writeResult(stdout, stderr, jsonMode, workspace.Report{Command: "version"}, usage(errors.New("version takes no positional arguments")))
		}
		return writeResult(stdout, stderr, jsonMode, workspace.Report{Command: "version", Message: "Workcopy " + Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ", " + runtime.Version() + ")"}, nil)
	}
	resolved, loadErr := loadSettings(parsed)
	if loadErr != nil {
		return writeResult(stdout, stderr, jsonMode, workspace.Report{Command: parsed.command}, loadErr)
	}
	if resolved.Bool(settings.JSON) && !parsed.boolean("json") {
		jsonMode = true
	}
	report, operationErr := execute(ctx, parsed, resolved)
	report.Command = parsed.command
	if report.Review != nil && !parsed.boolean("include-files") {
		// The per-file inventory of a review can run to megabytes. Leave it out
		// unless asked, as the MCP server always does, so an agent reading this
		// output is not flooded.
		summary := *report.Review
		summary.Files = map[string]model.FileVersion{}
		report.Review = &summary
	}
	return writeResult(stdout, stderr, jsonMode, report, operationErr)
}

func usage(err error) error { return model.Fail(2, "invalid-arguments", err.Error()) }

func writeResult(stdout, stderr io.Writer, jsonMode bool, report workspace.Report, err error) int {
	report.SchemaVersion = model.SchemaVersion
	result := response{Report: report}
	if err != nil {
		result.ExitCode = 5
		result.Error = &operationError{Code: "operation-failed", Message: err.Error()}
		var failure *model.Failure
		if errors.As(err, &failure) {
			result.ExitCode = failure.Exit
			result.Error = &operationError{Code: failure.Code, Message: failure.Message, Suggestion: failure.Suggestion}
		}
		if errors.Is(err, context.Canceled) {
			result.ExitCode = 3
			result.Error.Code = "cancelled"
		}
	}
	if jsonMode {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if encodeErr := encoder.Encode(result); encodeErr != nil {
			fmt.Fprintln(stderr, "Cannot write JSON:", encodeErr)
			return 5
		}
		return result.ExitCode
	}
	output := &checkedWriter{Writer: stdout}
	diagnostics := &checkedWriter{Writer: stderr}
	stdout, stderr = output, diagnostics
	if result.Error != nil {
		fmt.Fprintf(stderr, "%s: %s\n", result.Error.Code, result.Error.Message)
		if result.Error.Suggestion != "" {
			fmt.Fprintln(stderr, result.Error.Suggestion)
		}
	}
	if report.Message != "" {
		fmt.Fprintln(stdout, report.Message)
	}
	if report.Workspace != nil {
		fmt.Fprintf(stdout, "Workspace: %s\n", report.Workspace.Path)
	}
	for _, change := range report.Changes {
		fmt.Fprintf(stdout, "%-10s %s\n", change.Kind, change.Path)
	}
	for _, conflict := range report.Conflicts {
		fmt.Fprintf(stdout, "CONFLICT   %s\n  token: %s\n  reason: %s\n", conflict.Path, conflict.ID, conflict.Reason)
	}
	if report.Review != nil {
		fmt.Fprintf(stdout, "Review: %s\nReceipt: %s (%s)\n", report.Review.Path, report.Review.ID, report.Review.State)
	}
	if context := report.AgentContext; context != nil {
		if report.Command == "agent-context" {
			if len(context.Agents) > 0 {
				fmt.Fprintf(stdout, "Agents: %s (%s)\n", strings.Join(context.Agents, ", "), context.AgentsSource)
			}
			if len(context.NotSelected) > 0 {
				fmt.Fprintf(stdout, "Present but not served: %s\n", strings.Join(context.NotSelected, ", "))
			}
			for _, skipped := range context.Skipped {
				fmt.Fprintf(stderr, "Not copied: %s (%s)\n", skipped.Path, skipped.Reason)
			}
		}
		for _, candidate := range context.Candidates {
			fmt.Fprintf(stdout, "Candidate: %s (%d files, %s); add it with WORKCOPY_AGENT_CONTEXT_INCLUDE to copy it\n", candidate.Path, candidate.Files, candidate.Hint)
		}
		if report.Command == "agent-context" {
			for _, file := range context.Included {
				note := ""
				if file.Sensitive {
					note = " (may contain credentials)"
				}
				fmt.Fprintf(stdout, "%-10s %s [%s]%s\n", "include", file.Path, file.Source, note)
			}
			fmt.Fprintln(stdout, context.Guidance)
		}
	}
	for _, entry := range report.Settings {
		fmt.Fprintf(stdout, "%-40s %-24s [%s]\n", entry.Key, strings.Join(entry.Values, ";"), entry.Source)
	}
	for _, item := range report.Workspaces {
		if item.Phase != "" && item.Phase != "ready" {
			fmt.Fprintf(stdout, "%s\t%s\t[%s]\n", item.Name, item.Path, item.Phase)
			continue
		}
		fmt.Fprintf(stdout, "%s\t%s\n", item.Name, item.Path)
	}
	for _, warning := range report.Warnings {
		fmt.Fprintln(stderr, "Warning:", warning)
	}
	for _, next := range report.NextCommands {
		fmt.Fprintln(stdout, "Next:", next)
	}
	if report.Plan != nil {
		fmt.Fprintf(stdout, "Plan: %s; %d files; %d source bytes\n", report.Plan.Action, len(report.Plan.Files), report.Plan.SourceBytes)
	}
	if report.Content != nil {
		encoded, marshalErr := json.Marshal(report.Content)
		if marshalErr != nil {
			return 5
		}
		fmt.Fprintln(stdout, string(encoded))
	}
	if report.Catalog != nil {
		encoded, marshalErr := json.MarshalIndent(report.Catalog, "", "  ")
		if marshalErr != nil {
			return 5
		}
		fmt.Fprintln(stdout, string(encoded))
	}
	if output.err != nil || diagnostics.err != nil {
		return 5
	}
	return result.ExitCode
}

type checkedWriter struct {
	io.Writer
	err error
}

func (w *checkedWriter) Write(body []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.Writer.Write(body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}

// loadSettings resolves the user's preferences: flags, then environment, then the
// settings file, then built-in defaults.
func loadSettings(a arguments) (*settings.Resolved, error) {
	flags := map[string][]string{}
	for _, d := range settings.Definitions {
		if d.Flag != "" && len(a.values[d.Flag]) > 0 {
			flags[d.Key] = a.values[d.Flag]
		}
	}
	inputs := settings.DefaultInputs(a.value("settings"), flags)
	inputs.AllowMissing = a.command == "settings" && a.boolean("init")
	resolved, err := settings.Load(inputs)
	if err != nil {
		return nil, err
	}
	if err = resolved.ExportProcessEnvironment(os.LookupEnv, os.Setenv); err != nil {
		return nil, err
	}
	return resolved, nil
}

func execute(ctx context.Context, a arguments, resolved *settings.Resolved) (workspace.Report, error) {
	empty := workspace.Report{Command: a.command}
	allowed := map[string]string{
		"create": "branch profile project config all share trust-shared dependency-identity agent",
		"expand": "project", "status": "", "sync": "agent", "list": "", "destroy": "force",
		"resolve": "use-local use-upstream delete from-file expect-conflict expect-local",
		"merge":   "commit approve review message no-commit include-files", "content": "side offset limit",
		"recover": "abandon-review", "share": "path target dependency-identity trust-shared", "unshare": "path",
		"solution": "solution", "discover": "config", "doctor": "", "legacy": "", "settings": "init agent", "agent-context": "refresh agent",
	}
	flags, known := allowed[a.command]
	if !known {
		return empty, usage(fmt.Errorf("unknown command %q; run workcopy --help", a.command))
	}
	if err := a.allowed(flags); err != nil {
		return empty, usage(err)
	}
	for _, flag := range []string{"repo", "root", "workspace", "config", "target"} {
		if value := a.value(flag); value != "" {
			if err := fsops.CheckNativePath(value); err != nil {
				return empty, err
			}
		}
	}
	if a.command == "settings" {
		return showSettings(a, resolved)
	}
	if a.command == "doctor" {
		return doctor(ctx, a)
	}
	if a.command == "legacy" {
		return inspectLegacy(a)
	}
	repository, root, name := a.value("repo"), a.value("root"), a.value("name")
	var current *model.Manifest
	if a.value("workspace") != "" {
		manifest, err := workspace.Locate(a.value("workspace"))
		if err != nil {
			return empty, err
		}
		current = &manifest
	} else if repository == "" {
		directory, err := os.Getwd()
		if err != nil {
			return empty, err
		}
		if manifest, locateErr := workspace.Locate(directory); locateErr == nil {
			current = &manifest
		}
	}
	if current != nil {
		if repository == "" {
			repository = current.RepositoryRoot
		}
		if root == "" {
			root = current.WorkRoot
		}
		if name == "" {
			name = current.Name
		}
	}
	// Preferences fill in only what the command line and the lane did not.
	if repository == "" {
		repository = resolved.String(settings.Repo)
	}
	if root == "" {
		root = resolved.String(settings.Root)
	}
	if repository == "" {
		repository = "."
	}
	engine, err := workspace.New(ctx, repository, root)
	if err != nil {
		return empty, err
	}
	engine.Settings = resolved
	configFile := a.value("config")
	if configFile == "" {
		configFile = resolved.String(settings.Config)
	}
	positional := append([]string(nil), a.positionals...)
	if a.command != "list" && a.command != "discover" && name == "" && len(positional) > 0 {
		name, positional = positional[0], positional[1:]
	}
	if a.command != "list" && a.command != "discover" && a.command != "agent-context" && name == "" {
		return empty, usage(errors.New("specify a lane name, --name, or run from inside a Workcopy lane"))
	}
	dryRun := a.boolean("dry-run")
	switch a.command {
	case "create":
		if len(positional) != 0 {
			return empty, usage(errors.New("create takes one lane name; select projects with repeated --project"))
		}
		return engine.Create(ctx, name, workspace.CreateOptions{Branch: a.value("branch"), Profile: a.value("profile"), Projects: a.values["project"], All: a.boolean("all"), ConfigPath: configFile, Shares: a.values["share"], TrustShared: a.boolean("trust-shared"), DependencyIdentity: a.value("dependency-identity")}, dryRun)
	case "expand":
		return engine.Expand(ctx, name, append(a.values["project"], positional...), dryRun)
	case "resolve":
		if len(positional) != 1 {
			return empty, usage(errors.New("resolve requires exactly one repository-relative file path"))
		}
		return engine.Resolve(ctx, name, positional[0], workspace.ResolveOptions{UseLocal: a.boolean("use-local"), UseUpstream: a.boolean("use-upstream"), Delete: a.boolean("delete"), FromFile: a.value("from-file"), ExpectedConflict: a.value("expect-conflict"), ExpectedLocal: a.value("expect-local")}, dryRun)
	case "content":
		if len(positional) != 1 {
			return empty, usage(errors.New("content requires one conflict token"))
		}
		offset, numberErr := nonnegative(a.value("offset"), 0)
		if numberErr != nil {
			return empty, usage(numberErr)
		}
		limit, numberErr := nonnegative(a.value("limit"), 65536)
		if numberErr != nil {
			return empty, usage(numberErr)
		}
		if limit > 65536 {
			return empty, usage(errors.New("content limit must not exceed 65536 bytes"))
		}
		return engine.Content(ctx, name, positional[0], a.value("side"), offset, limit)
	}
	if len(positional) != 0 {
		return empty, usage(fmt.Errorf("unexpected positional arguments for %s", a.command))
	}
	switch a.command {
	case "status":
		return engine.Status(ctx, name)
	case "sync":
		return engine.Sync(ctx, name, dryRun)
	case "list":
		return engine.List(ctx)
	case "destroy":
		return engine.Destroy(ctx, name, a.boolean("force"), dryRun)
	case "merge":
		if a.boolean("commit") && a.boolean("no-commit") {
			return empty, usage(errors.New("--commit and --no-commit cannot be combined"))
		}
		return engine.Merge(ctx, name, workspace.MergeOptions{Commit: a.boolean("commit"), Approve: a.boolean("approve"), ReviewID: a.value("review"), Message: a.value("message")}, dryRun)
	case "recover":
		return engine.RecoverWith(ctx, name, workspace.RecoverOptions{AbandonReview: a.boolean("abandon-review")}, dryRun)
	case "share":
		return engine.Share(ctx, name, a.value("path"), a.value("target"), a.value("dependency-identity"), a.boolean("trust-shared"), dryRun)
	case "unshare":
		return engine.Unshare(ctx, name, a.value("path"), dryRun)
	case "solution":
		return engine.Solution(ctx, name, a.value("solution"), dryRun)
	case "discover":
		return engine.DiscoverProjects(ctx, configFile)
	case "agent-context":
		return engine.AgentContext(ctx, name, a.boolean("refresh"), dryRun)
	}
	return empty, usage(errors.New("unhandled command"))
}

func nonnegative(value string, fallback int64) (int64, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("expected a nonnegative integer, got %q", value)
	}
	return parsed, nil
}

const helpText = `Workcopy - lean Git-free working lanes and review-first promotion

Usage:
	workcopy mcp [--repo DIRECTORY] [--root WORKSPACE-ROOT]
	             [--config OWNER-CONFIG-FILE]
	             [--max-lanes N] [--reserve-mib N] [--tool-timeout DURATION]
	             [--allow-share-target DIRECTORY ...]
  workcopy create NAME [--branch REF] [--profile NAME | --all]
                      [--project ID-OR-PATH ...] [--config FILE] [--dry-run]
                      [--agent TOOL ...]
  workcopy expand NAME PROJECT-OR-PATH ...
  workcopy status NAME
  workcopy sync NAME [--dry-run] [--agent TOOL ...]
  workcopy resolve NAME PATH --expect-conflict TOKEN
                   (--use-local | --use-upstream | --delete | --from-file FILE)
  workcopy merge NAME [--dry-run] [--include-files]
  workcopy merge NAME --commit --approve --review RECEIPT --message MESSAGE
  workcopy content NAME TOKEN --side base|local|upstream [--offset N] [--limit N]
  workcopy share NAME --path PATH --target DIRECTORY --trust-shared
                      --dependency-identity IDENTITY [--dry-run]
  workcopy unshare NAME --path PATH [--dry-run]
  workcopy recover NAME [--abandon-review] [--dry-run]
  workcopy destroy NAME [--force] [--dry-run]
  workcopy list
  workcopy discover [--config FILE]
  workcopy solution NAME --solution FILE.sln [--dry-run]
  workcopy settings [--init] [--settings FILE]
  workcopy agent-context [NAME] [--refresh] [--dry-run] [--agent TOOL ...]
  workcopy doctor
  workcopy legacy --workspace OLD-LANE
  workcopy version

Global options: --repo DIRECTORY --root WORKSPACE-ROOT --name NAME
                --workspace LANE-DIRECTORY --settings FILE --json --help

Preferences (root, owner config, Git program, MCP limits, timeouts, agent
context) live in one settings file; flags override environment variables,
which override the file. Run "workcopy settings" to see every value.

Inside a lane, repository/root/name are inferred. Explicit flags win.
No command fetches, pushes, changes the main checkout, runs a package restore,
or silently duplicates dependency trees. Git is required. Active lanes are
not a security sandbox. Shared writable links require cooperative use.

Default merge creates an UNSTAGED separate Git review worktree. --no-commit
is a compatibility alias. A prepared review is not proof of integration.

Exit codes: 0 success; 1 unresolved conflicts; 2 invalid usage;
            3 blocked/stale precondition; 4 no change; 5 operation failure.
JSON mode writes one versioned object and uses the same exit code.
`
