package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"workcopy/internal/agents"
	"workcopy/internal/eol"
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
	// Agents selects the agent tools this lane serves (keys, "all" or "auto").
	// Empty uses the owner's setting, where "auto" follows the MCP client.
	Agents []string
}

// entryVersion describes a committed blob as a lane file. rule is the unresolved
// line-ending rule for the path; it only applies to regular files.
func entryVersion(entry git.TreeEntry, body []byte, rule string) (model.FileVersion, error) {
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
	if kind == model.Regular {
		version.Eol = eol.ForBlob(rule, body)
	}
	return version, nil
}

// entryVersionFrom describes a Git tree entry as a lane file, reading from the
// open blob reader only what it needs. A small blob is read whole and returned
// with the version. A large regular file is hashed as it streams and its body is
// nil: it is never held in memory, never line-ending converted (the version is of
// its raw bytes), and is written later straight from its Git object.
func entryVersionFrom(reader *git.Reader, entry git.TreeEntry, rule string) (model.FileVersion, []byte, error) {
	if (entry.Mode == 0100644 || entry.Mode == 0100755) && entry.Size > model.InlineFileBytes() {
		sum, size, err := reader.HashStream(entry.ObjectID)
		if err != nil {
			return model.FileVersion{}, nil, err
		}
		if size != entry.Size {
			return model.FileVersion{}, nil, fmt.Errorf("blob %s is %d bytes but its tree entry says %d", entry.ObjectID, size, entry.Size)
		}
		mode := uint32(0644)
		if entry.Mode == 0100755 {
			mode = 0755
		}
		return model.FileVersion{Kind: model.Regular, Mode: mode, Hash: sum, Size: size, ObjectID: entry.ObjectID}, nil, nil
	}
	body, err := reader.Read(entry.ObjectID)
	if err != nil {
		return model.FileVersion{}, nil, err
	}
	version, err := entryVersion(entry, body, rule)
	return version, body, err
}

func validateTree(tree map[string]git.TreeEntry, selection model.Selection, requirePaths bool) ([]string, error) {
	names := []string{}
	for name, entry := range tree {
		if !project.Selected(selection, name) {
			continue
		}
		if err := fsops.ValidateSourcePath(name); err != nil {
			return nil, model.Fail(3, "unportable-source-path", "A selected tracked path cannot be written safely on every supported platform ("+err.Error()+"). Workcopy refuses it rather than rename it: leave it out with --project or a profile, or rename it in the repository.")
		}
		if entry.Kind == "commit" {
			return nil, model.Fail(3, "unsupported-source-entry", "Selected path is a Git submodule, which lanes do not materialize: "+name+". Leave it out with --project or a profile.")
		}
		if entry.Kind != "blob" {
			return nil, model.Fail(3, "unsupported-source-entry", "Selected path is a special Git tree entry that lanes do not materialize: "+name)
		}
		if entry.Size > model.MaxFileBytes() {
			return nil, model.Fail(3, "unsupported-source-entry", fmt.Sprintf("Selected file is %d bytes, over the %d MiB file limit: %s. Raise WORKCOPY_MAX_FILE_MIB in the settings file, or leave the file out with --project or a profile.", entry.Size, model.MaxFileBytes()>>20, name))
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
	if err := validateAgents(options.Agents); err != nil {
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
	plan, err := e.checkoutPlan(ctx, commit, names)
	if err != nil {
		return report, err
	}
	destination := filepath.Join(e.WorkRoot, name)
	if _, err = os.Lstat(destination); err == nil {
		return report, model.Fail(3, "workspace-exists", "Destination already exists; nothing was replaced")
	} else if !os.IsNotExist(err) {
		return report, err
	}
	report.Plan = &Plan{Action: "create", Files: names, FileCount: len(names), Effects: []string{"Read the pinned commit, not the working checkout", "Materialize selected tracked source without a .git entry"}}
	for _, name := range names {
		report.Plan.SourceBytes += tree[name].Size
	}
	report.Warnings = catalog.Warnings
	if warning := LaneStorageWarning(e.WorkRoot); warning != "" {
		report.Warnings = append(report.Warnings, warning)
	}
	if len(options.Shares) > 0 {
		return report, model.Fail(2, "use-explicit-share", "Create the lane, then use share for each explicitly trusted dependency mapping")
	}
	if options.TrustShared || options.DependencyIdentity != "" {
		return report, model.Fail(2, "unused-sharing-options", "Sharing options belong to the share command")
	}
	if dryRun {
		if e.contextPolicy().Enabled {
			if preview, _, previewErr := e.contextPlan(ctx, nil, e.selectAgents(ctx, nil, options.Agents)); previewErr == nil && len(preview.Files) > 0 {
				report.Plan.Effects = append(report.Plan.Effects, fmt.Sprintf("Copy %d agent instruction files (%d bytes) from the main checkout", len(preview.Files), preview.TotalBytes))
			}
		}
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
	respect, err := e.executableBitsRespected(ctx, registry)
	if err != nil {
		return report, err
	}
	reader, err := e.Repository.Reader(ctx)
	if err != nil {
		return report, err
	}
	defer reader.Close()
	var contextAgents []string
	var creationSelection agents.Resolution
	if e.contextPolicy().Enabled {
		creationSelection = e.selectAgents(ctx, nil, options.Agents)
		contextAgents = recordSelection(creationSelection)
	}
	next := model.Snapshot{Manifest: model.Manifest{ContextAgents: contextAgents, SchemaVersion: 1, ID: id, Name: name, WorkspacePath: destination, RepositoryRoot: e.Repository.Root, CommonGitDir: e.Repository.CommonDir, WorkRoot: e.WorkRoot, Branch: branch, SyncedCommit: commit, Generation: 2, Phase: "ready", RespectExecutableBits: respect, OwnerConfig: options.ConfigData != nil || options.ConfigPath != "", Config: config, Selection: selection}, Baseline: model.Baseline{Files: map[string]model.FileVersion{}}, Conflicts: []model.Conflict{}}
	for _, filename := range names {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		version, body, versionErr := entryVersionFrom(reader, tree[filename], plan.rule(filename))
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
	// Fail before any pin, registration or directory exists, so a host that cannot
	// create symbolic links leaves nothing behind to recover.
	if collision, collisionErr := findNameCollision(registry, names); collisionErr != nil {
		return report, collisionErr
	} else if collision != nil {
		return report, nameCollisionFailure(collision)
	}
	for filename, version := range next.Baseline.Files {
		if version.Kind != model.Symlink {
			continue
		}
		if probeErr := registry.ProbeSymlink(); probeErr != nil {
			return report, model.Fail(3, "symlink-unavailable", "This selection tracks symbolic links (first: "+filename+") but this account cannot create them in "+e.WorkRoot+" ("+probeErr.Error()+"). On Windows enable Developer Mode or grant the 'Create symbolic links' right, or choose a lane root on an NTFS/ext4/APFS volume, or leave the linked paths out with --project or a profile. Nothing was created.")
		}
		break
	}
	if err = e.Repository.Pin(ctx, id, commit); err != nil {
		return report, err
	}
	record := registration{SchemaVersion: 1, ID: id, Name: name, CommonGitDir: e.Repository.CommonDir, Path: destination, Phase: "creating", Branch: branch, Seed: &next}
	if err = saveRegistration(registry, record); err != nil {
		return report, err
	}
	if err = registry.MkdirAll(name, 0700); err != nil {
		return report, err
	}
	if err = e.finishCreation(ctx, record); err != nil {
		if ctx.Err() != nil {
			// Whatever failed first, the operation was cut off, and what failed after
			// the cut (a staged object that was never written, a half-made directory)
			// is a consequence and not a cause. Report the cancellation alone, in a form
			// callers recognise, so a low-level file error never stands in for it.
			return report, fmt.Errorf("creation was interrupted and its recovery state was kept. Run recover to finish it; if recover keeps failing, the owner can discard the partly created lane with destroy %s --force: %w", name, ctx.Err())
		}
		return report, fmt.Errorf("creation did not finish and its recovery state was kept. Run recover to retry; if the cause is permanent (the message below says why), discard the partly created lane with destroy %s --force: %w", name, err)
	}
	record.Phase = "ready"
	record.Seed = nil
	if err = saveRegistration(registry, record); err != nil {
		return report, err
	}
	report.Workspace = view(next)
	report.Generation = next.Manifest.Generation
	report.Message = "Git-free lane created from the selected commit. No package restore was run."
	if e.contextPolicy().Enabled {
		summary, warning := e.copyInitialContext(ctx, name)
		report.AgentContext = summary
		if summary != nil {
			// The record says what was chosen; the report keeps why it was chosen.
			summary.AgentsSource = creationSelection.Source
		}
		if warning != "" {
			report.Warnings = append(report.Warnings, warning)
		} else if summary != nil && len(summary.Added) > 0 {
			report.Message += fmt.Sprintf(" Copied %d agent instruction files from the main checkout.", len(summary.Added))
		}
	}
	return report, nil
}

// findNameCollision asks the lane volume whether any two selected paths are one
// entry on it. It is a variable so a test can stand in for a volume (macOS) that
// the host running the tests does not have.
var findNameCollision = func(volume *fsops.Root, names []string) (*fsops.NameCollision, error) {
	return volume.FindNameCollision(names)
}

// nameCollisionFailure explains two tracked paths that the lane volume stores
// as one entry. The spellings are quoted with escapes because the usual cause,
// the same accented name written two ways, looks identical on screen.
func nameCollisionFailure(collision *fsops.NameCollision) error {
	pair := strconv.QuoteToASCII(collision.Second)
	if collision.First != "" {
		pair = strconv.QuoteToASCII(collision.First) + " and " + pair
	} else {
		pair += " and an earlier path"
	}
	return model.Fail(3, "name-collision", "Two tracked paths are the same file on the volume that would hold the lane: "+pair+". This happens when a volume treats canonically equivalent Unicode spellings as one name (macOS does) or ignores case. Nothing was created. Leave one of them out with --project or a profile, rename one in the repository, or choose a lane root on a volume that keeps them apart.")
}

func validateLink(name, target string) error {
	unsafe := func(reason string) error {
		return model.Fail(3, "unsafe-source-link", "Tracked symbolic link "+name+" is refused ("+reason+"). Links must be relative and stay inside the lane; leave the path out with --project or a profile.")
	}
	if filepath.IsAbs(target) || strings.HasPrefix(target, "/") || strings.Contains(target, "\\") || (len(target) >= 2 && target[1] == ':') {
		return unsafe("absolute or backslash target")
	}
	if err := fsops.ValidateSourcePath(filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(filepath.FromSlash(name)), target)))); err != nil {
		return unsafe(err.Error())
	}
	return nil
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
	objects := newGitObjects(ctx, e.Repository)
	defer objects.Close()
	store.Objects = objects
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
	names := []string{}
	for name := range record.Seed.Baseline.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	// Contents come straight from Git when the transaction is applied, and
	// progress is reported as they are read, which is the write phase itself.
	objects.progress = func(done int) { reportProgress(ctx, "Creating lane files", done, len(names)) }
	reportProgress(ctx, "Creating lane files", 0, len(names))
	for _, name := range names {
		if err = tx.AddFromObject(name, model.FileVersion{Kind: model.Missing}, record.Seed.Baseline.Files[name]); err != nil {
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
	plan, err := e.checkoutPlan(ctx, opened.snapshot.Manifest.SyncedCommit, additions)
	if err != nil {
		return report, err
	}
	report.Plan = &Plan{Action: "expand", Files: additions, FileCount: len(additions)}
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
		version, body, versionErr := entryVersionFrom(reader, tree[filename], plan.rule(filename))
		if versionErr != nil {
			return report, versionErr
		}
		if _, readErr := opened.root.Lstat(filename); readErr == nil {
			return report, model.Fail(3, "expansion-obstructed", "Expansion would overwrite existing local content: "+filename)
		} else if !os.IsNotExist(readErr) {
			return report, readErr
		}
		next.Baseline.Files[filename] = version
		if !version.Large() {
			if _, err = opened.store.PutBlob(body); err != nil {
				return report, err
			}
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
