// Package mcpserver adapts the workspace engine to a local MCP server.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workcopy/internal/fsops"
	"workcopy/internal/git"
	"workcopy/internal/model"
	"workcopy/internal/workspace"
)

type Options struct {
	Root         string
	MaxLanes     int
	ReserveBytes uint64
	ShareTargets []string
	ConfigPath   string
}

type Server struct {
	engine *workspace.Engine
	opts   Options
	// Serialize mutations per root. Filesystem locks
	// remain authoritative across processes; arbitrary editors are not locked.
	writer      sync.Mutex
	activeRoots map[string]bool
	protocol    *mcp.Server
	configData  []byte
	configErr   error
}

type Page struct {
	Offset int `json:"offset,omitempty" jsonschema:"Collection offset, default 0"`
	Limit  int `json:"limit,omitempty" jsonschema:"Maximum items per collection, default 100, maximum 200"`
}
type PageInfo struct {
	Total      int `json:"total"`
	NextOffset int `json:"nextOffset"`
}
type Failure struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}
type Output struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Scope         *Scope              `json:"scope,omitempty"`
	ExitCode      int                 `json:"exitCode"`
	Report        *workspace.Report   `json:"report,omitempty"`
	Context       *Context            `json:"context,omitempty"`
	Error         *Failure            `json:"error,omitempty"`
	Pages         map[string]PageInfo `json:"pages,omitempty"`
}
type Scope struct {
	Repo string `json:"repo,omitempty" jsonschema:"Absolute source checkout path; defaults to startup repository when configured"`
	Root string `json:"root,omitempty" jsonschema:"Exact absolute lane root; use returned scope for subsequent calls"`
}
type Empty struct {
	Page
	Scope
}
type Bootstrap struct {
	Empty
	Directory string `json:"directory,omitempty" jsonschema:"Absolute current directory; identify a source checkout or an existing lane without knowing its repo/root"`
}
type Lane struct {
	Page
	Scope
	Name string `json:"name" jsonschema:"Registered lane name, not a filesystem path"`
}
type Mutation struct {
	Lane
	DryRun bool `json:"dryRun" jsonschema:"Preview without persistent side effects"`
}
type Create struct {
	Mutation
	Branch   string   `json:"branch,omitempty"`
	Profile  string   `json:"profile,omitempty"`
	Projects []string `json:"projects,omitempty"`
}
type Expand struct {
	Mutation
	Projects []string `json:"projects"`
}
type Resolve struct {
	Mutation
	Path          string `json:"path"`
	Token         string `json:"token"`
	Mode          string `json:"mode" jsonschema:"local, upstream, delete, or file"`
	FromFile      string `json:"fromFile,omitempty" jsonschema:"Repository-relative lane file containing reviewed resolution"`
	ExpectedLocal string `json:"expectedLocal,omitempty"`
}
type Content struct {
	Lane
	Token      string `json:"token"`
	Side       string `json:"side"`
	ByteOffset int64  `json:"byteOffset,omitempty"`
	ByteLimit  int64  `json:"byteLimit,omitempty"`
}
type Solution struct {
	Mutation
	Path string `json:"path"`
}
type Share struct {
	Mutation
	Path               string `json:"path"`
	Target             string `json:"target" jsonschema:"Exact owner-allowlisted shared directory"`
	DependencyIdentity string `json:"dependencyIdentity"`
	TrustShared        bool   `json:"trustShared" jsonschema:"Explicit consent to writable cooperative sharing"`
}

func New(engine *workspace.Engine, version string, opts Options) *Server {
	s := &Server{engine: engine, opts: opts}
	if opts.ConfigPath != "" {
		absolute, err := filepath.Abs(opts.ConfigPath)
		if err == nil {
			s.opts.ConfigPath = filepath.Clean(absolute)
			var root *fsops.Root
			root, err = fsops.OpenRoot(filepath.Dir(absolute))
			if err == nil {
				s.configData, err = root.ReadFileLimit(filepath.Base(absolute), 1<<20)
				_ = root.Close()
				if s.configData == nil {
					s.configData = []byte{}
				}
			}
		}
		s.configErr = err
	}
	s.protocol = mcp.NewServer(&mcp.Implementation{Name: "workcopy", Version: version}, &mcp.ServerOptions{Instructions: Instructions, Capabilities: &mcp.ServerCapabilities{}, PageSize: 100})
	add(s, "doctor", "Report runtime, Git and filesystem backend without changing the repository or running scripts.", true, func(ctx context.Context, s *Server, in Empty) (Output, error) {
		gitPath, err := exec.LookPath("git")
		if err != nil {
			return s.result(workspace.Report{}, err, in.Page), nil
		}
		cmd := exec.CommandContext(ctx, gitPath, "--version")
		cmd.Env = git.Environment()
		body, err := cmd.Output()
		return s.result(workspace.Report{Command: "doctor", Message: fmt.Sprintf("Workcopy %s; %s %s/%s; %s; %s", version, runtime.Version(), runtime.GOOS, runtime.GOARCH, string(body), fsops.BackendDescription)}, err, in.Page), nil
	})
	add(s, "bootstrap", "Start here: pass repo or your current directory. Identify an existing lane or discover a checkout, build inputs and storage headroom. Does not run builds or restores.", true, func(ctx context.Context, s *Server, in Bootstrap) (Output, error) {
		if in.Directory != "" {
			if manifest, err := workspace.Locate(in.Directory); err == nil {
				return s.laneContext(ctx, Lane{Name: manifest.Name, Page: in.Page})
			}
		}
		return s.bootstrap(ctx, in.Page)
	})
	add(s, "discover", "Discover committed projects/profiles and dependency warnings. Use returned IDs and explicit dependency closure.", true, func(ctx context.Context, s *Server, in Empty) (Output, error) {
		r, e := s.discover(ctx)
		return s.result(r, e, in.Page), nil
	})
	add(s, "list", "List owned lanes in the selected repository/root. Pagination applies to workspaces.", true, func(ctx context.Context, s *Server, in Empty) (Output, error) {
		r, e := s.engine.List(ctx)
		return s.result(r, e, in.Page), nil
	})
	add(s, "lane_context", "Confirm lane identity, selection, instructions, build inputs and validation candidates. Never executes repository code.", true, func(ctx context.Context, s *Server, in Lane) (Output, error) { return s.laneContext(ctx, in) })
	add(s, "status", "Inspect source changes, generation and durable conflict tokens. Clean source status is not build success or integration.", true, func(ctx context.Context, s *Server, in Lane) (Output, error) {
		r, e := s.engine.Status(ctx, in.Name)
		return s.result(r, e, in.Page), nil
	})
	add(s, "create", "Create a uniquely named Git-free lane from committed source. Explicit projects or profile required. Preview before apply; storage admission may block.", false, func(ctx context.Context, s *Server, in Create) (Output, error) {
		if s.configErr != nil {
			return s.result(workspace.Report{}, s.configErr, in.Page), nil
		}
		if in.Profile == "" && len(in.Projects) == 0 {
			return s.result(workspace.Report{}, model.Fail(2, "selection-required", "Choose projects or a configured profile; discover first"), in.Page), nil
		}
		_, commit, err := s.engine.Repository.Resolve(ctx, in.Branch)
		if err != nil {
			return s.result(workspace.Report{}, err, in.Page), nil
		}
		options := workspace.CreateOptions{Branch: in.Branch, Profile: in.Profile, Projects: in.Projects, ConfigData: s.configData, ExpectedCommit: commit}
		r, e := s.engine.Create(ctx, in.Name, options, true)
		if e == nil && !in.DryRun {
			e = s.admit(ctx, r.Plan.SourceBytes, len(r.Plan.Files), true)
			if e == nil {
				r, e = s.engine.Create(ctx, in.Name, options, false)
			}
		}
		return s.result(r, e, in.Page), nil
	})
	add(s, "expand", "Add selected projects without replacing local edits. Repeated expansion may return no-change (4).", false, func(ctx context.Context, s *Server, in Expand) (Output, error) {
		r, e := s.engine.Expand(ctx, in.Name, in.Projects, true)
		if e == nil && !in.DryRun {
			if r.Plan != nil && len(r.Plan.Files) > 0 {
				e = s.admit(ctx, r.Plan.SourceBytes, len(r.Plan.Files), false)
			}
			if e == nil {
				r, e = s.engine.Expand(ctx, in.Name, in.Projects, false)
			}
		}
		return s.result(r, e, in.Page), nil
	})
	add(s, "sync", "Three-way reconcile lane/local/upstream. Returns unresolved conflicts (1); stop editors first.", false, func(ctx context.Context, s *Server, in Mutation) (Output, error) {
		if !in.DryRun {
			if err := s.admitLane(ctx, in.Name, false); err != nil {
				return s.result(workspace.Report{}, err, in.Page), nil
			}
		}
		r, e := s.engine.Sync(ctx, in.Name, in.DryRun)
		return s.result(r, e, in.Page), nil
	})
	add(s, "resolve", "Resolve one current conflict, preserving accepted upstream and local contribution separately. File mode reads only an owned lane file.", false, func(ctx context.Context, s *Server, in Resolve) (Output, error) { return s.resolve(ctx, in) })
	add(s, "content", "Read base64 conflict bytes, at most 65536 per call. Assemble chunks and verify reported whole-content SHA256. Explicitly exposes source bytes.", true, func(ctx context.Context, s *Server, in Content) (Output, error) {
		if in.ByteLimit == 0 {
			in.ByteLimit = 65536
		}
		r, e := s.engine.Content(ctx, in.Name, in.Token, in.Side, in.ByteOffset, in.ByteLimit)
		return s.result(r, e, in.Page), nil
	})
	add(s, "review", "Prepare a separate unstaged human review worktree. Full repository source consumes disk. Does not commit, push or integrate.", false, func(ctx context.Context, s *Server, in Mutation) (Output, error) {
		if !in.DryRun {
			if e := s.admitReview(ctx, in.Name); e != nil {
				return s.result(workspace.Report{}, e, in.Page), nil
			}
		}
		r, e := s.engine.Merge(ctx, in.Name, workspace.MergeOptions{}, in.DryRun)
		return s.result(r, e, in.Page), nil
	})
	add(s, "solution", "Generate a selected-project .slnf from a lane .sln. For .slnx use selected project files directly. Not a build verdict.", false, func(ctx context.Context, s *Server, in Solution) (Output, error) {
		r, e := s.engine.Solution(ctx, in.Name, in.Path, in.DryRun)
		return s.result(r, e, in.Page), nil
	})
	add(s, "share", "Create a writable dependency link only to an owner-allowlisted target, with explicit trust and environment identity. Not read-only isolation.", false, func(ctx context.Context, s *Server, in Share) (Output, error) {
		allowed := false
		for _, target := range s.opts.ShareTargets {
			if filepath.Clean(in.Target) == target {
				allowed = true
				break
			}
		}
		if !allowed {
			return s.result(workspace.Report{}, model.Fail(3, "share-target-not-allowed", "Owner must start MCP with --allow-share-target for this exact directory; no automatic copying fallback"), in.Page), nil
		}
		r, e := s.engine.Share(ctx, in.Name, in.Path, in.Target, in.DependencyIdentity, in.TrustShared, in.DryRun)
		return s.result(r, e, in.Page), nil
	})
	add(s, "unshare", "Remove only an owned managed link, retaining its shared target. Does not delete dependency packages.", false, func(ctx context.Context, s *Server, in Solution) (Output, error) {
		r, e := s.engine.Unshare(ctx, in.Name, in.Path, in.DryRun)
		return s.result(r, e, in.Page), nil
	})
	add(s, "recover", "Recover a journalled interrupted operation with existing stale-write and ownership guards. Never remove/edit journals manually.", false, func(ctx context.Context, s *Server, in Mutation) (Output, error) {
		r, e := s.engine.Recover(ctx, in.Name, in.DryRun)
		return s.result(r, e, in.Page), nil
	})
	add(s, "destroy", "Guarded cleanup of an owned clean lane. Preview first; preserves unreviewed/ignored local files and shared targets. Force is unavailable.", false, func(ctx context.Context, s *Server, in Mutation) (Output, error) {
		r, e := s.engine.Destroy(ctx, in.Name, false, in.DryRun)
		return s.result(r, e, in.Page), nil
	})
	s.protocol.AddResource(&mcp.Resource{URI: "workcopy://guide", Name: "Workcopy agent operating guide", MIMEType: "text/plain"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "workcopy://guide", MIMEType: "text/plain", Text: Instructions}}}, nil
	})
	for name, text := range map[string]string{"workcopy_controller": controllerPrompt, "workcopy_worker": workerPrompt} {
		s.protocol.AddPrompt(&mcp.Prompt{Name: name, Description: "Safe independent-lane operating contract"}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}}}, nil
		})
	}
	return s
}

func add[I any](s *Server, name, description string, readOnly bool, handler func(context.Context, *Server, I) (Output, error)) {
	closed := false
	mcp.AddTool(s.protocol, &mcp.Tool{Name: "workcopy_" + name, Description: description, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, OpenWorldHint: &closed}}, func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, Output, error) {
		// Validate common pagination before any mutation.
		body, _ := json.Marshal(in)
		var page Page
		_ = json.Unmarshal(body, &page)
		if page.Offset < 0 || page.Limit < 0 || page.Limit > 200 {
			out := s.result(workspace.Report{}, model.Fail(2, "invalid-page", "offset must be nonnegative and limit must be 0..200"), Page{})
			return &mcp.CallToolResult{IsError: true}, out, nil
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		var scope Scope
		_ = json.Unmarshal(body, &scope)
		if name == "bootstrap" {
			var input Bootstrap
			_ = json.Unmarshal(body, &input)
			if input.Directory != "" {
				var err error
				scope, err = directoryScope(input.Directory, scope)
				if err != nil {
					return &mcp.CallToolResult{IsError: true}, s.result(workspace.Report{}, err, page), nil
				}
			}
		}
		selected, err := s.selectScope(ctx, scope, name == "doctor")
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, s.result(workspace.Report{}, err, page), nil
		}
		if !readOnly {
			if !s.beginMutation(selected.engine.WorkRoot) {
				return &mcp.CallToolResult{IsError: true}, s.result(workspace.Report{}, model.Fail(3, "workspace-busy", "Another MCP mutation is running in this root; coordinate writers and reread status before retrying"), page), nil
			}
			defer s.endMutation(selected.engine.WorkRoot)
			var mutation struct{ DryRun bool }
			_ = json.Unmarshal(body, &mutation)
			if !mutation.DryRun && (name == "resolve" || name == "solution" || name == "share") {
				if err := selected.admit(ctx, 0, 0, false); err != nil {
					return &mcp.CallToolResult{IsError: true}, selected.result(workspace.Report{}, err, page), nil
				}
			}
		}
		out, err := handler(ctx, selected, in)
		if err != nil {
			out = s.result(workspace.Report{}, err, page)
		}
		if selected.engine != nil {
			out.Scope = &Scope{Repo: selected.engine.Repository.Root, Root: selected.engine.WorkRoot}
		}
		return &mcp.CallToolResult{IsError: out.ExitCode != 0 && out.ExitCode != 4}, out, nil
	})
}

func (s *Server) result(report workspace.Report, err error, page Page) Output {
	report.SchemaVersion = model.SchemaVersion
	out := Output{SchemaVersion: 1, ExitCode: report.ExitCode, Report: &report, Pages: map[string]PageInfo{}}
	if err != nil {
		out.ExitCode = 5
		out.Error = &Failure{Code: "operation-failed", Message: err.Error()}
		var failure *model.Failure
		if errors.As(err, &failure) {
			out.ExitCode = failure.Exit
			out.Error = &Failure{failure.Code, failure.Message, failure.Suggestion}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			out.ExitCode = 3
			out.Error.Code = "cancelled"
		}
	}
	report.ExitCode = out.ExitCode
	report.Changes = paginate(report.Changes, "changes", page, out.Pages)
	report.Conflicts = paginate(report.Conflicts, "conflicts", page, out.Pages)
	report.Workspaces = paginate(report.Workspaces, "workspaces", page, out.Pages)
	report.Warnings = paginate(report.Warnings, "warnings", page, out.Pages)
	if report.Plan != nil {
		copy := *report.Plan
		copy.Files = append([]string{}, paginate(copy.Files, "files", page, out.Pages)...)
		report.Plan = &copy
	}
	if report.Catalog != nil {
		copy := *report.Catalog
		copy.Projects = append([]model.Project{}, paginate(copy.Projects, "projects", page, out.Pages)...)
		copy.Warnings = paginate(copy.Warnings, "catalogWarnings", page, out.Pages)
		report.Catalog = &copy
	}
	if report.Review != nil {
		copy := *report.Review
		copy.Files = map[string]model.FileVersion{}
		report.Review = &copy
	}
	// Discovery config may contain a duplicate unbounded project catalog. Return
	// profile names separately in bootstrap; CLI retains its original contract.
	report.Config = nil
	return out
}
func paginate[T any](items []T, key string, page Page, pages map[string]PageInfo) []T {
	limit := page.Limit
	if limit == 0 {
		limit = 100
	}
	start := min(page.Offset, len(items))
	end := min(start+limit, len(items))
	next := 0
	if end < len(items) {
		next = end
	}
	pages[key] = PageInfo{len(items), next}
	return items[start:end]
}

func (s *Server) Run(ctx context.Context, transport mcp.Transport) error {
	return s.protocol.Run(ctx, transport)
}

func (s *Server) resolve(ctx context.Context, in Resolve) (Output, error) {
	options := workspace.ResolveOptions{ExpectedConflict: in.Token, ExpectedLocal: in.ExpectedLocal}
	switch in.Mode {
	case "local":
		options.UseLocal = true
	case "upstream":
		options.UseUpstream = true
	case "delete":
		options.Delete = true
	case "file":
		if in.FromFile == "" {
			return s.result(workspace.Report{}, model.Fail(2, "resolution-file-required", "file mode requires a repository-relative lane file"), in.Page), nil
		}
		options.FromFile = in.FromFile
		options.FromLaneFile = true
	default:
		return s.result(workspace.Report{}, model.Fail(2, "invalid-resolution-mode", "mode must be local, upstream, delete or file"), in.Page), nil
	}
	if in.Mode != "file" && in.FromFile != "" {
		return s.result(workspace.Report{}, model.Fail(2, "invalid-resolution-file", "fromFile requires file mode"), in.Page), nil
	}
	r, e := s.engine.Resolve(ctx, in.Name, in.Path, options, in.DryRun)
	return s.result(r, e, in.Page), nil
}
