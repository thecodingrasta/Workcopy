// Package workspace implements Git-free lanes and review-first promotion.
package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"workcopy/internal/fsops"
	"workcopy/internal/git"
	"workcopy/internal/model"
	"workcopy/internal/project"
	"workcopy/internal/state"
)

type Engine struct {
	Repository *git.Repository
	WorkRoot   string
}
type WorkspaceView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Phase  string `json:"phase"`
}
type Change struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}
type Plan struct {
	Action      string   `json:"action"`
	Files       []string `json:"files"`
	SourceBytes int64    `json:"sourceBytes"`
	Effects     []string `json:"effects,omitempty"`
}
type Content struct {
	Side       string `json:"side"`
	Encoding   string `json:"encoding"`
	Data       string `json:"data"`
	Offset     int64  `json:"offset"`
	Total      int64  `json:"total"`
	NextOffset int64  `json:"nextOffset"`
	SHA256     string `json:"sha256"`
}
type Report struct {
	SchemaVersion int              `json:"schemaVersion"`
	ExitCode      int              `json:"exitCode"`
	Command       string           `json:"command"`
	Message       string           `json:"message,omitempty"`
	Workspace     *WorkspaceView   `json:"workspace,omitempty"`
	Generation    uint64           `json:"generation,omitempty"`
	Changes       []Change         `json:"changes,omitempty"`
	Conflicts     []model.Conflict `json:"conflicts,omitempty"`
	Review        *model.Review    `json:"review,omitempty"`
	Workspaces    []WorkspaceView  `json:"workspaces,omitempty"`
	Warnings      []string         `json:"warnings,omitempty"`
	NextCommands  []string         `json:"nextCommands,omitempty"`
	Plan          *Plan            `json:"plan,omitempty"`
	Content       *Content         `json:"content,omitempty"`
	Catalog       *project.Catalog `json:"catalog,omitempty"`
	Config        *model.Config    `json:"config,omitempty"`
}
type registration struct {
	SchemaVersion int                          `json:"schemaVersion"`
	ID            string                       `json:"id"`
	Name          string                       `json:"name"`
	CommonGitDir  string                       `json:"commonGitDir"`
	Path          string                       `json:"path"`
	Phase         string                       `json:"phase"`
	Seed          *model.Snapshot              `json:"seed,omitempty"`
	Quarantine    string                       `json:"quarantine,omitempty"`
	DeleteFiles   map[string]model.FileVersion `json:"deleteFiles,omitempty"`
	DeleteDirs    []string                     `json:"deleteDirs,omitempty"`
	Force         bool                         `json:"force,omitempty"`
}
type lane struct {
	root     *fsops.Root
	store    *state.Store
	snapshot model.Snapshot
	record   registration
}

func (l *lane) close() { l.root.Close() }

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

func validateName(name string) error {
	if !validName.MatchString(name) {
		return model.Fail(2, "invalid-name", "Lane names must be 1-64 letters, digits, dots, underscores or hyphens, starting with a letter or digit")
	}
	return fsops.ValidateSourcePath(name)
}
func canonical(directory string) (string, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(absolute)
	if parent == absolute {
		return "", err
	}
	base, err := canonical(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, filepath.Base(absolute)), nil
}
func noGitAncestor(directory string) error {
	for current := directory; ; current = filepath.Dir(current) {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return model.Fail(3, "workspace-inside-repository", "Workspace roots and lanes must be outside Git checkouts")
		} else if !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}
func New(ctx context.Context, repository, root string) (*Engine, error) {
	r, err := git.Open(ctx, repository)
	if err != nil {
		return nil, err
	}
	if root == "" {
		root = filepath.Join(filepath.Dir(r.Root), ".workcopy-"+filepath.Base(r.Root)+"-"+model.Hash([]byte(r.CommonDir))[:8])
	}
	root, err = canonical(root)
	if err != nil {
		return nil, err
	}
	if containsPath(root, r.Root) || containsPath(r.Root, root) || containsPath(r.CommonDir, root) {
		return nil, model.Fail(3, "unsafe-work-root", "Workspace root must not contain or be inside the source repository")
	}
	if err = noGitAncestor(root); err != nil {
		return nil, err
	}
	return &Engine{r, root}, nil
}
func (e *Engine) rootLock() (*fsops.Root, *state.Lock, error) {
	if err := noGitAncestor(e.WorkRoot); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(e.WorkRoot, 0700); err != nil {
		return nil, nil, err
	}
	root, err := fsops.OpenRoot(e.WorkRoot)
	if err != nil {
		return nil, nil, err
	}
	lock, err := state.Acquire(root, ".workcopy-registry/lock")
	if err != nil {
		root.Close()
		return nil, nil, err
	}
	return root, lock, nil
}
func registrationPath(name string) string { return ".workcopy-registry/" + name + ".json" }
func (e *Engine) readRegistration(name string) (registration, error) {
	var record registration
	if err := validateName(name); err != nil {
		return record, err
	}
	root, err := fsops.OpenRoot(e.WorkRoot)
	if err != nil {
		return record, err
	}
	defer root.Close()
	body, err := root.ReadFileLimit(registrationPath(name), model.MaxMetadataBytes)
	if err != nil {
		return record, model.Fail(3, "workspace-not-registered", "No owned lane is registered with this name and workspace root")
	}
	if err = model.DecodeStrict(body, &record); err != nil {
		return record, err
	}
	if record.SchemaVersion != 1 || record.Name != name || record.Path != filepath.Join(e.WorkRoot, name) || record.CommonGitDir != e.Repository.CommonDir || !model.ValidID(record.ID) {
		return record, model.Fail(3, "ownership-mismatch", "Registration identity does not match this lane and repository")
	}
	return record, nil
}
func saveRegistration(root *fsops.Root, record registration) error {
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if int64(len(body)) > model.MaxMetadataBytes {
		return fmt.Errorf("registration exceeds size limit")
	}
	return root.WriteAtomic(registrationPath(record.Name), body, 0600)
}
func (e *Engine) openLane(name string) (*lane, error) {
	record, err := e.readRegistration(name)
	if err != nil {
		return nil, err
	}
	if record.Phase != "ready" {
		return nil, model.Fail(3, "recovery-required", "Lane creation or deletion is incomplete; run recover")
	}
	root, err := fsops.OpenRoot(record.Path)
	if err != nil {
		return nil, err
	}
	store := state.New(root)
	snapshot, err := store.Load()
	if err != nil {
		root.Close()
		return nil, err
	}
	m := snapshot.Manifest
	if m.ID != record.ID || m.Name != record.Name || m.WorkspacePath != record.Path || m.WorkRoot != e.WorkRoot || m.CommonGitDir != e.Repository.CommonDir {
		root.Close()
		return nil, model.Fail(3, "ownership-mismatch", "Registry and lane metadata disagree")
	}
	if m.Phase != "ready" {
		root.Close()
		return nil, model.Fail(3, "workspace-not-ready", "Lane is not ready; run recover")
	}
	if _, err = root.Lstat(state.JournalPath); err == nil {
		root.Close()
		return nil, model.Fail(3, "recovery-required", "An interrupted transaction exists; run recover")
	} else if !os.IsNotExist(err) {
		root.Close()
		return nil, err
	}
	for _, receipt := range m.Reviews {
		if receipt.State == "preparing" || receipt.State == "committing" {
			root.Close()
			return nil, model.Fail(3, "recovery-required", "A review operation is incomplete; run recover")
		}
	}
	return &lane{root, store, snapshot, record}, nil
}
func (e *Engine) reloadLocked(opened *lane) (*state.Lock, error) {
	lock, err := opened.store.Lock()
	if err != nil {
		return nil, err
	}
	fresh, err := e.openLane(opened.record.Name)
	if err != nil {
		lock.Close()
		return nil, err
	}
	defer fresh.close()
	opened.snapshot = fresh.snapshot
	return lock, nil
}
func newGeneration(snapshot model.Snapshot) model.Snapshot {
	next := snapshot
	next.Manifest.Generation++
	next.Baseline.Files = maps.Clone(snapshot.Baseline.Files)
	next.Conflicts = append([]model.Conflict(nil), snapshot.Conflicts...)
	next.Manifest.Mappings = append([]model.Mapping(nil), snapshot.Manifest.Mappings...)
	next.Manifest.Reviews = append([]model.Review(nil), snapshot.Manifest.Reviews...)
	return next
}
func view(s model.Snapshot) *WorkspaceView {
	return &WorkspaceView{s.Manifest.ID, s.Manifest.Name, s.Manifest.WorkspacePath, s.Manifest.Branch, s.Manifest.Phase}
}
func (e *Engine) configuration(ctx context.Context, tree map[string]git.TreeEntry, filename string, data []byte) (model.Config, error) {
	config := model.Config{SchemaVersion: 1, Discovery: model.Discovery{Adapters: []string{"auto"}}}
	var body []byte
	var err error
	if data != nil {
		if filename != "" {
			return config, model.Fail(2, "ambiguous-configuration", "Choose a configuration path or pinned contents, not both")
		}
		body = data
	} else if filename != "" {
		absolute, pathErr := filepath.Abs(filename)
		if pathErr != nil {
			return config, pathErr
		}
		root, openErr := fsops.OpenRoot(filepath.Dir(absolute))
		if openErr != nil {
			return config, openErr
		}
		defer root.Close()
		body, err = root.ReadFileLimit(filepath.Base(absolute), 1<<20)
	} else if entry, ok := tree[".workcopy.json"]; ok {
		if entry.Mode == 0120000 {
			return config, fmt.Errorf("configuration cannot be a symlink")
		}
		reader, readErr := e.Repository.Reader(ctx)
		if readErr != nil {
			return config, readErr
		}
		defer reader.Close()
		body, err = reader.Read(entry.ObjectID)
	} else {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	if len(body) > 1<<20 {
		return config, fmt.Errorf("configuration exceeds 1 MiB")
	}
	config = model.Config{}
	if err = model.DecodeStrict(body, &config); err != nil {
		return config, err
	}
	if config.SchemaVersion != 1 {
		return config, fmt.Errorf("unsupported configuration schema")
	}
	return config, nil
}
func (e *Engine) scan(ctx context.Context, opened *lane) (map[string]model.FileVersion, error) {
	candidates, err := e.candidates(ctx, opened)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for name := range opened.snapshot.Baseline.Files {
		names[name] = true
	}
	for _, name := range candidates {
		if project.Selected(opened.snapshot.Manifest.Selection, name) {
			names[name] = true
		}
	}
	current := map[string]model.FileVersion{}
	for name := range names {
		if name == ".workcopy" || strings.HasPrefix(name, ".workcopy/") {
			continue
		}
		mapped := false
		for _, mapping := range opened.snapshot.Manifest.Mappings {
			if name == mapping.Path || strings.HasPrefix(name, mapping.Path+"/") {
				mapped = true
				break
			}
		}
		if mapped {
			continue
		}
		if err = fsops.ValidateSourcePath(name); err != nil {
			return nil, err
		}
		version, _, readErr := state.ReadVersion(opened.root, name, opened.snapshot.Baseline.Files[name], opened.snapshot.Manifest.RespectExecutableBits)
		if readErr != nil {
			return nil, readErr
		}
		current[name] = version
	}
	liveNames := []string{}
	for name, version := range current {
		if version.Exists() {
			liveNames = append(liveNames, name)
		}
	}
	sort.Strings(liveNames)
	if err = validatePortableNames(liveNames); err != nil {
		return nil, err
	}
	return current, nil
}
func changes(base, current map[string]model.FileVersion) []Change {
	keys := map[string]bool{}
	for name := range base {
		keys[name] = true
	}
	for name := range current {
		keys[name] = true
	}
	result := []Change{}
	for name := range keys {
		before, after := base[name], current[name]
		if before.Equal(after) {
			continue
		}
		kind := "modified"
		if !before.Exists() {
			kind = "added"
		} else if !after.Exists() {
			kind = "deleted"
		} else if before.Kind != after.Kind {
			kind = "type-changed"
		}
		result = append(result, Change{name, kind})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}
func (e *Engine) Status(ctx context.Context, name string) (Report, error) {
	report := Report{Command: "status"}
	opened, err := e.openLane(name)
	if err != nil {
		return report, err
	}
	defer opened.close()
	current, err := e.scan(ctx, opened)
	if err != nil {
		return report, err
	}
	report.Workspace = view(opened.snapshot)
	report.Generation = opened.snapshot.Manifest.Generation
	report.Changes = changes(opened.snapshot.Baseline.Files, current)
	report.Conflicts = opened.snapshot.Conflicts
	report.Message = "Status describes source changes, not build success or integration."
	report.Warnings, err = e.SharedWarnings(ctx, name)
	if err != nil {
		return report, err
	}
	if len(report.Conflicts) > 0 {
		report.ExitCode = 1
	}
	return report, nil
}
func (e *Engine) List(ctx context.Context) (Report, error) {
	report := Report{Command: "list", Workspaces: []WorkspaceView{}}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	directory := filepath.Join(e.WorkRoot, ".workcopy-registry")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		record, readErr := e.readRegistration(name)
		if readErr != nil {
			return report, readErr
		}
		report.Workspaces = append(report.Workspaces, WorkspaceView{ID: record.ID, Name: name, Path: record.Path, Phase: record.Phase})
	}
	return report, nil
}
func Locate(directory string) (model.Manifest, error) {
	current, err := canonical(directory)
	if err != nil {
		return model.Manifest{}, err
	}
	for {
		root, openErr := fsops.OpenRoot(current)
		if openErr != nil {
			return model.Manifest{}, openErr
		}
		s, loadErr := state.New(root).Load()
		root.Close()
		if loadErr == nil {
			if s.Manifest.WorkspacePath != current {
				return model.Manifest{}, model.Fail(3, "ownership-mismatch", "Workspace was moved outside its registration")
			}
			return s.Manifest, nil
		}
		if !os.IsNotExist(loadErr) {
			return model.Manifest{}, loadErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return model.Manifest{}, model.Fail(3, "not-in-workspace", "No Workcopy lane was found in this directory or its ancestors")
}

func (e *Engine) candidates(ctx context.Context, opened *lane) ([]string, error) {
	excluded := make([]string, 0, len(opened.snapshot.Manifest.Mappings))
	for _, mapping := range opened.snapshot.Manifest.Mappings {
		excluded = append(excluded, mapping.Path)
	}
	return e.Repository.Candidates(ctx, opened.root.Path, excluded...)
}
