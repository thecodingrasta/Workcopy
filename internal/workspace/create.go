package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"workcopy/internal/fsops"
	"workcopy/internal/git"
	"workcopy/internal/model"
	"workcopy/internal/project"
	"workcopy/internal/state"
)

type CreateOptions struct {
	Branch, Profile, ConfigPath string
	ExpectedCommit              string
	// ConfigData pins an explicit owner configuration in memory. Nil uses
	// ConfigPath or the committed configuration, preserving CLI behavior.
	ConfigData         []byte
	Projects           []string
	All                bool
	Shares             []string
	TrustShared        bool
	DependencyIdentity string
}

func entryVersion(entry git.TreeEntry, body []byte) (model.FileVersion, error) {
	kind, mode := model.Regular, uint32(0644)
	switch entry.Mode {
	case 0100644:
	case 0100755:
		mode = 0755
	case 0120000:
		kind = model.Symlink
		mode = 0120000
	default:
		return model.FileVersion{}, model.Fail(3, "unsupported-tree-entry", "Git submodules and special tree entries are not materialized")
	}
	version := model.Version(kind, mode, body)
	version.ObjectID = entry.ObjectID
	return version, nil
}
func validateTree(tree map[string]git.TreeEntry, selection model.Selection, requirePaths bool) ([]string, error) {
	names := []string{}
	for name, entry := range tree {
		if !project.Selected(selection, name) {
			continue
		}
		if err := fsops.ValidateSourcePath(name); err != nil {
			return nil, err
		}
		if entry.Kind != "blob" || entry.Size > model.MaxFileBytes {
			return nil, model.Fail(3, "unsupported-source-entry", "Selected path is a submodule, special entry or exceeds 64 MiB: "+name)
		}
		names = append(names, name)
	}
	if err := validatePortableNames(names); err != nil {
		return nil, err
	}
	for _, wanted := range selection.Paths {
		if !requirePaths {
			break
		}
		found := false
		for _, name := range names {
			if project.Match(wanted, name) {
				found = true
				break
			}
		}
		if !found {
			return nil, model.Fail(2, "project-not-found", "Selected project or path contains no included tracked source: "+wanted)
		}
	}
	sort.Strings(names)
	return names, nil
}
func (e *Engine) Create(ctx context.Context, name string, options CreateOptions, dryRun bool) (Report, error) {
	report := Report{Command: "create"}
	if err := validateName(name); err != nil {
		return report, err
	}
	if options.All && (options.Profile != "" || len(options.Projects) > 0) {
		return report, model.Fail(2, "ambiguous-selection", "--all cannot be combined with --profile or --project")
	}
	branch, commit, err := e.Repository.Resolve(ctx, options.Branch)
	if err != nil {
		return report, err
	}
	if options.ExpectedCommit != "" && commit != options.ExpectedCommit {
		return report, model.Fail(3, "source-changed", "Source ref advanced after storage admission; inspect the new snapshot and retry")
	}
	tree, err := e.Repository.Tree(ctx, commit)
	if err != nil {
		return report, err
	}
	config, err := e.configuration(ctx, tree, options.ConfigPath, options.ConfigData)
	if err != nil {
		return report, err
	}
	catalog, err := project.Discover(ctx, e.Repository, tree, config)
	if err != nil {
		return report, err
	}
	config.Projects = catalog.Projects
	selection, err := project.Select(config, catalog, options.Profile, options.Projects, options.All)
	if err != nil {
		return report, err
	}
	names, err := validateTree(tree, selection, true)
	if err != nil {
		return report, err
	}
	if err = e.validateAttributes(ctx, commit, names); err != nil {
		return report, err
	}
	destination := filepath.Join(e.WorkRoot, name)
	if _, err = os.Lstat(destination); err == nil {
		return report, model.Fail(3, "workspace-exists", "Destination already exists; nothing was replaced")
	} else if !os.IsNotExist(err) {
		return report, err
	}
	report.Plan = &Plan{Action: "create", Files: names, Effects: []string{"Read the pinned commit, not the working checkout", "Materialize selected tracked source without a .git entry"}}
	for _, name := range names {
		report.Plan.SourceBytes += tree[name].Size
	}
	report.Warnings = catalog.Warnings
	if len(options.Shares) > 0 {
		return report, model.Fail(2, "use-explicit-share", "Create the lane, then use share for each explicitly trusted dependency mapping")
	}
	if options.TrustShared || options.DependencyIdentity != "" {
		return report, model.Fail(2, "unused-sharing-options", "Sharing options belong to the share command")
	}
	if dryRun {
		report.Message = "Creation planned; no directories, state or Git refs were written."
		return report, nil
	}
	registry, rootLock, err := e.rootLock()
	if err != nil {
		return report, err
	}
	defer registry.Close()
	defer rootLock.Close()
	if _, err = registry.Lstat(registrationPath(name)); err == nil {
		return report, model.Fail(3, "workspace-exists", "Lane name is already registered")
	} else if !os.IsNotExist(err) {
		return report, err
	}
	if _, err = registry.Lstat(name); err == nil {
		return report, model.Fail(3, "workspace-exists", "Destination appeared after planning")
	} else if !os.IsNotExist(err) {
		return report, err
	}
	id, err := model.ID()
	if err != nil {
		return report, err
	}
	reader, err := e.Repository.Reader(ctx)
	if err != nil {
		return report, err
	}
	defer reader.Close()
	next := model.Snapshot{Manifest: model.Manifest{SchemaVersion: 1, ID: id, Name: name, WorkspacePath: destination, RepositoryRoot: e.Repository.Root, CommonGitDir: e.Repository.CommonDir, WorkRoot: e.WorkRoot, Branch: branch, SyncedCommit: commit, Generation: 2, Phase: "ready", RespectExecutableBits: runtime.GOOS != "windows", Config: config, Selection: selection}, Baseline: model.Baseline{Files: map[string]model.FileVersion{}}, Conflicts: []model.Conflict{}}
	for _, filename := range names {
		body, readErr := reader.Read(tree[filename].ObjectID)
		if readErr != nil {
			return report, readErr
		}
		version, versionErr := entryVersion(tree[filename], body)
		if versionErr != nil {
			return report, versionErr
		}
		if version.Kind == model.Symlink {
			if err = validateLink(filename, string(body)); err != nil {
				return report, err
			}
		}
		next.Baseline.Files[filename] = version
	}
	if err = e.Repository.Pin(ctx, id, commit); err != nil {
		return report, err
	}
	record := registration{SchemaVersion: 1, ID: id, Name: name, CommonGitDir: e.Repository.CommonDir, Path: destination, Phase: "creating", Seed: &next}
	if err = saveRegistration(registry, record); err != nil {
		return report, err
	}
	if err = registry.MkdirAll(name, 0700); err != nil {
		return report, err
	}
	if err = e.finishCreation(ctx, record); err != nil {
		return report, fmt.Errorf("creation preserved its recovery state; run recover: %w", err)
	}
	record.Phase = "ready"
	record.Seed = nil
	if err = saveRegistration(registry, record); err != nil {
		return report, err
	}
	report.Workspace = view(next)
	report.Generation = next.Manifest.Generation
	report.Message = "Git-free lane created from the selected commit. No package restore was run."
	return report, nil
}
func validateLink(name, target string) error {
	if filepath.IsAbs(target) || strings.Contains(target, "\\") {
		return fmt.Errorf("source link must stay inside the lane: %s", name)
	}
	return fsops.ValidateSourcePath(filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(filepath.FromSlash(name)), target))))
}
func (e *Engine) finishCreation(ctx context.Context, record registration) error {
	if record.Seed == nil {
		return fmt.Errorf("creation seed is missing")
	}
	root, err := fsops.OpenRoot(record.Path)
	if err != nil {
		return err
	}
	defer root.Close()
	store := state.New(root)
	if _, err = root.Lstat(state.ManifestPath); os.IsNotExist(err) {
		seed := *record.Seed
		seed.Manifest.Generation = 1
		seed.Manifest.Phase = "creating"
		seed.Baseline.Files = map[string]model.FileVersion{}
		if err = store.Save(seed); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	lock, err := store.Lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	snapshot, err := store.Load()
	if err != nil {
		return err
	}
	if snapshot.Manifest.ID != record.ID {
		return fmt.Errorf("creation identity mismatch")
	}
	if _, err = root.Lstat(state.JournalPath); err == nil {
		return store.Recover()
	} else if !os.IsNotExist(err) {
		return err
	}
	if snapshot.Manifest.Phase == "ready" {
		return nil
	}
	tx, err := store.Begin(*record.Seed)
	if err != nil {
		return err
	}
	reader, err := e.Repository.Reader(ctx)
	if err != nil {
		return err
	}
	defer reader.Close()
	names := []string{}
	for name := range record.Seed.Baseline.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		version := record.Seed.Baseline.Files[name]
		body, readErr := reader.Read(version.ObjectID)
		if readErr != nil {
			return readErr
		}
		if err = tx.Add(name, model.FileVersion{Kind: model.Missing}, version, body, false); err != nil {
			return err
		}
	}
	if err = tx.Prepare(); err != nil {
		return err
	}
	return tx.Apply()
}
func (e *Engine) Expand(ctx context.Context, name string, requested []string, dryRun bool) (Report, error) {
	report := Report{Command: "expand"}
	if len(requested) == 0 {
		return report, model.Fail(2, "projects-required", "Specify at least one project ID or relative source path")
	}
	opened, err := e.openLane(name)
	if err != nil {
		return report, err
	}
	defer opened.close()
	if !dryRun {
		lock, lockErr := e.reloadLocked(opened)
		if lockErr != nil {
			return report, lockErr
		}
		defer lock.Close()
	}
	if len(opened.snapshot.Conflicts) > 0 {
		return report, model.Fail(3, "conflicts-pending", "Resolve existing conflicts before expanding")
	}
	if opened.snapshot.Manifest.Selection.All {
		report.ExitCode = 4
		report.Message = "The lane already includes all tracked source."
		return report, nil
	}
	tree, err := e.Repository.Tree(ctx, opened.snapshot.Manifest.SyncedCommit)
	if err != nil {
		return report, err
	}
	catalog := project.Catalog{Projects: opened.snapshot.Manifest.Config.Projects}
	combined := append(append([]string(nil), opened.snapshot.Manifest.Selection.Projects...), opened.snapshot.Manifest.Selection.Paths...)
	combined = append(combined, requested...)
	selection, err := project.Select(opened.snapshot.Manifest.Config, catalog, "", combined, false)
	if err != nil {
		return report, err
	}
	selection.Include = opened.snapshot.Manifest.Selection.Include
	selection.Exclude = opened.snapshot.Manifest.Selection.Exclude
	names, err := validateTree(tree, selection, true)
	if err != nil {
		return report, err
	}
	additions := []string{}
	for _, filename := range names {
		if _, ok := opened.snapshot.Baseline.Files[filename]; !ok {
			additions = append(additions, filename)
		}
	}
	if err = e.validateAttributes(ctx, opened.snapshot.Manifest.SyncedCommit, additions); err != nil {
		return report, err
	}
	report.Plan = &Plan{Action: "expand", Files: additions}
	for _, filename := range additions {
		report.Plan.SourceBytes += tree[filename].Size
	}
	if dryRun {
		return report, nil
	}
	if len(additions) == 0 {
		report.ExitCode = 4
		report.Message = "No new tracked source was selected."
		return report, nil
	}
	next := newGeneration(opened.snapshot)
	next.Manifest.Selection = selection
	reader, err := e.Repository.Reader(ctx)
	if err != nil {
		return report, err
	}
	defer reader.Close()
	type addition struct {
		name    string
		version model.FileVersion
	}
	values := []addition{}
	for _, filename := range additions {
		for _, mapping := range next.Manifest.Mappings {
			if filename == mapping.Path || strings.HasPrefix(filename, mapping.Path+"/") {
				return report, model.Fail(3, "mapping-overlaps-source", "Detach the dependency mapping before expanding into it")
			}
		}
		body, readErr := reader.Read(tree[filename].ObjectID)
		if readErr != nil {
			return report, readErr
		}
		version, versionErr := entryVersion(tree[filename], body)
		if versionErr != nil {
			return report, versionErr
		}
		if _, readErr = opened.root.Lstat(filename); readErr == nil {
			return report, model.Fail(3, "expansion-obstructed", "Expansion would overwrite existing local content: "+filename)
		} else if !os.IsNotExist(readErr) {
			return report, readErr
		}
		next.Baseline.Files[filename] = version
		if _, err = opened.store.PutBlob(body); err != nil {
			return report, err
		}
		values = append(values, addition{filename, version})
	}
	tx, err := opened.store.Begin(next)
	if err != nil {
		return report, err
	}
	for _, v := range values {
		if err = tx.AddStaged(v.name, model.FileVersion{}, v.version, false); err != nil {
			return report, err
		}
	}
	if err = tx.Prepare(); err != nil {
		return report, err
	}
	if err = tx.Apply(); err != nil {
		return report, err
	}
	report.Workspace = view(next)
	report.Generation = next.Manifest.Generation
	report.Message = "Selection expanded from the lane's accepted snapshot; existing edits were preserved."
	return report, nil
}

// Prefixes are checked as well as leaves: Src/a and src/b collide on Windows.
func validatePortableNames(names []string) error {
	caseNames := map[string]string{}
	for _, name := range names {
		if err := fsops.ValidateSourcePath(name); err != nil {
			return err
		}
		parts := strings.Split(name, "/")
		for n := 1; n <= len(parts); n++ {
			prefix := strings.Join(parts[:n], "/")
			fold := strings.ToLower(prefix)
			if previous := caseNames[fold]; previous != "" && previous != prefix {
				return model.Fail(3, "case-collision", "Source paths collide on case-insensitive filesystems: "+previous+" and "+prefix)
			}
			caseNames[fold] = prefix
		}
	}
	return nil
}
