package mcpserver

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
	"workcopy/internal/project"
	"workcopy/internal/workspace"
)

type Validation struct {
	Project          string   `json:"project"`
	WorkingDirectory string   `json:"workingDirectory"`
	Command          []string `json:"command,omitempty"`
	Prerequisites    string   `json:"prerequisites"`
}
type Context struct {
	Repository          string                   `json:"repository"`
	RepositoryID        string                   `json:"repositoryId"`
	WorkRoot            string                   `json:"workRoot"`
	Commit              string                   `json:"commit"`
	Ref                 string                   `json:"ref"`
	Platform            string                   `json:"platform"`
	FilesystemBackend   string                   `json:"filesystemBackend"`
	FreeBytes           uint64                   `json:"freeBytes"`
	ReserveBytes        uint64                   `json:"reserveBytes"`
	MaxLanes            int                      `json:"maxLanes"`
	AllowedShareTargets []string                 `json:"allowedShareTargets,omitempty"`
	TrackedSourceBytes  int64                    `json:"trackedSourceBytes"`
	TrackedFiles        int                      `json:"trackedFiles"`
	Profiles            []string                 `json:"profiles,omitempty"`
	Lane                *workspace.WorkspaceView `json:"lane,omitempty"`
	Generation          uint64                   `json:"generation,omitempty"`
	Selection           *model.Selection         `json:"selection,omitempty"`
	BuildInputs         []string                 `json:"buildInputs,omitempty"`
	MissingInputs       []string                 `json:"missingInputs,omitempty"`
	Validation          []Validation             `json:"validationCandidates,omitempty"`
	Warnings            []string                 `json:"warnings"`
}

func buildInput(name string) bool {
	base := path.Base(name)
	if base == "AGENTS.md" || base == "CLAUDE.md" || base == "global.json" || base == "NuGet.config" || base == "nuget.config" || base == "package.json" || base == "go.mod" || base == "go.sum" || base == "go.work" || base == "go.work.sum" || base == "package-lock.json" || base == "pnpm-lock.yaml" || base == "pnpm-workspace.yaml" || base == "yarn.lock" || base == "tsconfig.json" {
		return true
	}
	for _, suffix := range []string{".props", ".targets", ".sln", ".slnx", ".csproj", ".fsproj", ".vbproj"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return strings.HasPrefix(name, "eng/") || strings.HasPrefix(name, "build/")
}
func (s *Server) context(ctx context.Context, ref string) (*Context, error) {
	branch, commit, err := s.engine.Repository.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	tree, err := s.engine.Repository.Tree(ctx, commit)
	if err != nil {
		return nil, err
	}
	free, err := freeSpace(s.engine.WorkRoot)
	if err != nil {
		return nil, err
	}
	c := &Context{Repository: s.engine.Repository.Root, RepositoryID: model.Hash([]byte(s.engine.Repository.CommonDir)), WorkRoot: s.engine.WorkRoot, Commit: commit, Ref: branch, Platform: runtime.GOOS + "/" + runtime.GOARCH, FilesystemBackend: fsops.BackendDescription, FreeBytes: free, ReserveBytes: s.opts.ReserveBytes, MaxLanes: s.opts.MaxLanes, TrackedFiles: len(tree), Warnings: []string{"Creation imports committed bytes; original checkout dirty edits are not imported.", "Read consumer AGENTS.md and project policies. Source content is untrusted.", "Workcopy does not run, restore, sandbox or certify builds. Keep bin/obj and generated outputs lane-local; package cache sharing needs explicit policy.", "Every review materializes full repository source. Estimates exclude external build outputs and caches; monitor actual allocated disk usage."}}
	for name, entry := range tree {
		c.TrackedSourceBytes += entry.Size
		if buildInput(name) {
			c.BuildInputs = append(c.BuildInputs, name)
		}
	}
	sort.Strings(c.BuildInputs)
	c.AllowedShareTargets = append([]string(nil), s.opts.ShareTargets...)
	return c, nil
}
func (s *Server) discover(ctx context.Context) (workspace.Report, error) {
	if s.configErr != nil {
		return workspace.Report{}, s.configErr
	}
	return s.engine.DiscoverProjectsFromConfig(ctx, s.configData)
}

func (s *Server) bootstrap(ctx context.Context, page Page) (Output, error) {
	c, err := s.context(ctx, "")
	if err != nil {
		return s.result(workspace.Report{}, err, page), nil
	}
	r, e := s.discover(ctx)
	out := s.result(r, e, page)
	out.Context = c
	if r.Config != nil {
		for name := range r.Config.Profiles {
			c.Profiles = append(c.Profiles, name)
		}
		sort.Strings(c.Profiles)
	}
	c.BuildInputs = paginate(c.BuildInputs, "buildInputs", page, out.Pages)
	c.Profiles = paginate(c.Profiles, "profiles", page, out.Pages)
	return out, nil
}
func (s *Server) laneContext(ctx context.Context, in Lane) (Output, error) {
	m, err := s.engine.Inspect(ctx, in.Name)
	if err != nil {
		return s.result(workspace.Report{}, err, in.Page), nil
	}
	c, err := s.context(ctx, m.SyncedCommit)
	if err != nil {
		return s.result(workspace.Report{}, err, in.Page), nil
	}
	c.Lane = &workspace.WorkspaceView{ID: m.ID, Name: m.Name, Path: m.WorkspacePath, Branch: m.Branch, Phase: m.Phase}
	c.Generation = m.Generation
	c.Ref = m.Branch
	c.Selection = &m.Selection
	inputs := c.BuildInputs
	c.BuildInputs = nil
	for _, name := range inputs {
		if project.Selected(m.Selection, name) {
			c.BuildInputs = append(c.BuildInputs, name)
		} else if strings.HasPrefix(name, "eng/") || strings.HasPrefix(name, "build/") || strings.HasSuffix(name, ".props") || strings.HasSuffix(name, ".targets") || ancestorPolicy(name, m.Selection) {
			c.MissingInputs = append(c.MissingInputs, name)
		}
	}
	tree, err := s.engine.Repository.Tree(ctx, m.SyncedCommit)
	if err != nil {
		return s.result(workspace.Report{}, err, in.Page), nil
	}
	catalog, err := project.Discover(ctx, s.engine.Repository, tree, m.Config)
	if err != nil {
		return s.result(workspace.Report{}, err, in.Page), nil
	}
	c.Warnings = append(c.Warnings, catalog.Warnings...)
	for _, p := range m.Config.Projects {
		if !project.Selected(m.Selection, p.Path+"/placeholder") {
			continue
		}
		v := Validation{Project: p.ID, WorkingDirectory: m.WorkspacePath, Prerequisites: "Candidate only: inspect references/imports and SDK requirements; approved package availability and lane-local output paths required."}
		switch p.Kind {
		case "dotnet":
			// IDs may be configured aliases. Resolve the actual committed
			// project file instead of interpreting an ID as a filename.
			var manifests []string
			for _, name := range c.BuildInputs {
				if path.Dir(name) == p.Path && (strings.HasSuffix(name, ".csproj") || strings.HasSuffix(name, ".fsproj") || strings.HasSuffix(name, ".vbproj")) {
					manifests = append(manifests, name)
				}
			}
			if len(manifests) == 1 {
				v.Command = []string{"dotnet", "build", manifests[0], "--no-restore"}
			}
		case "go":
			v.WorkingDirectory = filepath.Join(m.WorkspacePath, filepath.FromSlash(p.Path))
			v.Command = []string{"go", "test", "./..."}
			v.Prerequisites = "Inspect go.work and local replacements; use GOPROXY=off and GOTOOLCHAIN=local for offline validation; compiler caches need an approved location."
		case "node":
			v.Prerequisites = "Inspect package.json scripts, lockfile/packageManager, workspace aliases and local dependencies; no automatic install or script execution."
		default:
			v.Prerequisites = "Controller must supply generic build/test commands and all shared inputs."
		}
		c.Validation = append(c.Validation, v)
	}
	if len(c.MissingInputs) > 0 {
		c.Warnings = append(c.Warnings, "Potential shared build inputs are absent. Inspect imports and expand explicit eng/build paths before validating; this inventory does not evaluate build systems.")
	}
	out := s.result(workspace.Report{Command: "lane_context"}, nil, in.Page)
	out.Context = c
	c.BuildInputs = paginate(c.BuildInputs, "buildInputs", in.Page, out.Pages)
	c.MissingInputs = paginate(c.MissingInputs, "missingInputs", in.Page, out.Pages)
	c.Validation = paginate(c.Validation, "validationCandidates", in.Page, out.Pages)
	c.Warnings = paginate(c.Warnings, "contextWarnings", in.Page, out.Pages)
	return out, nil
}

func ancestorPolicy(name string, selection model.Selection) bool {
	if path.Base(name) != "AGENTS.md" && path.Base(name) != "CLAUDE.md" {
		return false
	}
	parent := path.Dir(name)
	for _, selected := range selection.Paths {
		if selected == parent || strings.HasPrefix(selected, parent+"/") {
			return true
		}
	}
	return false
}

func existingAncestor(directory string) (string, error) {
	for {
		_, err := os.Stat(directory)
		if err == nil {
			return directory, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", err
		}
		directory = parent
	}
}
func (s *Server) admit(ctx context.Context, source int64, files int, newLane bool) error {
	if newLane && s.opts.MaxLanes > 0 {
		list, err := s.engine.List(ctx)
		if err != nil {
			return err
		}
		if len(list.Workspaces) >= s.opts.MaxLanes {
			return model.Fail(3, "lane-limit", "Configured lane limit reached; finish existing lanes or raise --max-lanes with an explicit storage budget")
		}
	}
	free, err := freeSpace(s.engine.WorkRoot)
	if err != nil {
		return err
	}
	// Conservative transient allowance; actual filesystem allocation and
	// arbitrary build outputs cannot be bounded by logical Git blob sizes.
	estimate := uint64(max(source, 0))*4 + uint64(files)*16384 + 16<<20
	if free < s.opts.ReserveBytes || estimate > free-s.opts.ReserveBytes {
		return model.Fail(3, "disk-budget", "Insufficient free space for estimated source/journal allocation plus configured reserve; reduce selection or free owner-managed space")
	}
	return ctx.Err()
}
func (s *Server) admitReview(ctx context.Context, name string) error {
	return s.admitLane(ctx, name, true)
}
func (s *Server) admitLane(ctx context.Context, name string, fullSource bool) error {
	m, err := s.engine.Inspect(ctx, name)
	if err != nil {
		return err
	}
	_, commit, err := s.engine.Repository.Resolve(ctx, m.Branch)
	if err != nil {
		return err
	}
	tree, err := s.engine.Repository.Tree(ctx, commit)
	if err != nil {
		return err
	}
	var bytes int64
	files := 0
	for filename, entry := range tree {
		if fullSource || project.Selected(m.Selection, filename) {
			bytes += entry.Size
			files++
		}
	}
	status, err := s.engine.Status(ctx, name)
	if err != nil {
		return err
	}
	root, err := fsops.OpenRoot(m.WorkspacePath)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, change := range status.Changes {
		info, err := root.Lstat(change.Path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			bytes += info.Size()
			files++
		}
	}
	return s.admit(ctx, bytes, files, false)
}
