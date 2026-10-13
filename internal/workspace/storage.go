package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
	"workcopy/internal/state"
)

// Share registers an explicitly trusted, cooperative dependency directory. It
// never copies a dependency tree or claims that a writable link is read-only.
func (e *Engine) Share(ctx context.Context, name, relative, target, identity string, trust, dryRun bool) (Report, error) {
	report := Report{Command: "share"}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := fsops.ValidateSourcePath(relative); err != nil {
		return report, err
	}
	if !trust || strings.TrimSpace(identity) == "" {
		return report, model.Fail(2, "shared-storage-consent-required", "Specify --trust-shared and a nonempty --dependency-identity describing the prepared dependency environment")
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		return report, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return report, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return report, err
	}
	if !info.IsDir() {
		return report, model.Fail(3, "dependency-target-not-directory", "The shared dependency target must be an existing directory")
	}
	if containsPath(e.WorkRoot, absolute) || containsPath(absolute, e.WorkRoot) || containsPath(absolute, e.Repository.Root) {
		return report, model.Fail(3, "dependency-target-inside-workspaces", "Dependency targets must be outside the managed workspace root and cannot contain it or the source checkout")
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
	if opened.snapshot.Manifest.Phase != "ready" {
		return report, model.Fail(3, "workspace-not-ready", "Finish recovery before changing dependency mappings")
	}
	for source := range opened.snapshot.Baseline.Files {
		if source == relative || strings.HasPrefix(source, relative+"/") || strings.HasPrefix(relative, source+"/") {
			return report, model.Fail(3, "mapping-overlaps-source", "Dependency mappings cannot overlap tracked source: "+source)
		}
	}
	for _, mapping := range opened.snapshot.Manifest.Mappings {
		if mapping.Path == relative || strings.HasPrefix(mapping.Path, relative+"/") || strings.HasPrefix(relative, mapping.Path+"/") {
			return report, model.Fail(3, "mapping-overlap", "A managed mapping already overlaps this path")
		}
	}
	if _, err = opened.root.Lstat(relative); err == nil {
		return report, model.Fail(3, "mapping-destination-exists", "The destination already exists; Workcopy will not replace or copy it")
	} else if !os.IsNotExist(err) {
		return report, err
	}
	inputs, err := e.dependencyInputs(ctx, opened)
	if err != nil {
		return report, err
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	fingerprintBytes, err := json.Marshal(struct {
		Inputs             map[string]string
		Platform, Identity string
	}{inputs, platform, identity})
	if err != nil {
		return report, err
	}
	mapping := model.Mapping{Path: relative, Target: absolute, Strategy: fsops.DirectoryLinkStrategy, Protection: "cooperative-writable", Inputs: inputs, Platform: platform, DependencyIdentity: identity, Fingerprint: model.Hash(fingerprintBytes)}
	report.Plan = &Plan{Action: "share", Files: []string{relative}, Effects: []string{"Create one managed directory link; no dependency files are copied", "Record declared dependency identity and current manifest/lockfile fingerprints"}}
	report.Warnings = []string{"This is a writable shared directory, not a security boundary. Both lanes and the source may affect the same files.", "The declared dependency identity is owner-supplied. Workcopy detects input drift but does not certify package-manager ABI compatibility."}
	if dryRun {
		report.Message = "Shared dependency mapping planned; no link or metadata was created."
		return report, nil
	}
	next := newGeneration(opened.snapshot)
	next.Manifest.Mappings = append(next.Manifest.Mappings, mapping)
	sort.Slice(next.Manifest.Mappings, func(i, j int) bool { return next.Manifest.Mappings[i].Path < next.Manifest.Mappings[j].Path })
	transaction, err := opened.store.Begin(next)
	if err != nil {
		return report, err
	}
	body := []byte(absolute)
	if err = transaction.Add(relative, model.FileVersion{Kind: model.Missing}, model.Version(model.Symlink, 0120000, body), body, true); err != nil {
		return report, err
	}
	if err = transaction.Prepare(); err != nil {
		return report, err
	}
	if err = transaction.Apply(); err != nil {
		return report, fmt.Errorf("dependency link could not be created; no copy fallback was attempted; recover the owned journal before continuing: %w", err)
	}
	report.Workspace = view(next)
	report.Generation = next.Manifest.Generation
	report.Message = "Managed shared dependency link created; no dependency tree was copied."
	return report, nil
}

// Unshare removes only the owned link. It never traverses the dependency target.
func (e *Engine) Unshare(ctx context.Context, name, relative string, dryRun bool) (Report, error) {
	report := Report{Command: "unshare"}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := fsops.ValidateSourcePath(relative); err != nil {
		return report, err
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
	index := -1
	for candidate, mapping := range opened.snapshot.Manifest.Mappings {
		if mapping.Path == relative {
			index = candidate
			break
		}
	}
	if index < 0 {
		return report, model.Fail(3, "mapping-not-owned", "No owned dependency mapping exists at this path")
	}
	mapping := opened.snapshot.Manifest.Mappings[index]
	actual, err := opened.root.Readlink(relative)
	if err != nil {
		return report, err
	}
	if actual != mapping.Target {
		return report, model.Fail(3, "mapping-retargeted", "The mapping target changed; nothing was removed")
	}
	if dryRun {
		report.Message = "Would remove the owned link only; the target directory is untouched."
		report.Plan = &Plan{Action: "unshare", Files: []string{relative}}
		return report, nil
	}
	next := newGeneration(opened.snapshot)
	next.Manifest.Mappings = append(next.Manifest.Mappings[:index], next.Manifest.Mappings[index+1:]...)
	transaction, err := opened.store.Begin(next)
	if err != nil {
		return report, err
	}
	before := model.Version(model.Symlink, 0120000, []byte(actual))
	if err = transaction.Add(relative, before, model.FileVersion{Kind: model.Missing}, nil, false); err != nil {
		return report, err
	}
	if err = transaction.Prepare(); err != nil {
		return report, err
	}
	if err = transaction.Apply(); err != nil {
		return report, err
	}
	report.Workspace = view(next)
	report.Generation = next.Manifest.Generation
	report.Message = "Owned dependency link removed; the shared directory was not traversed or deleted."
	return report, nil
}

func containsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func dependencyFile(name string) bool {
	base := path.Base(name)
	switch base {
	case "package.json", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "pnpm-workspace.yaml", ".npmrc", "go.mod", "go.sum", "go.work", "go.work.sum", "packages.config", "packages.lock.json", "Directory.Packages.props", "Directory.Build.props", "Directory.Build.targets", "global.json", "NuGet.Config", "nuget.config", "pyproject.toml", "poetry.lock", "uv.lock", "Pipfile", "Pipfile.lock", "Cargo.toml", "Cargo.lock", "Gemfile", "Gemfile.lock", "composer.json", "composer.lock", "pom.xml", "build.gradle", "build.gradle.kts", "gradle.lockfile":
		return true
	}
	return strings.HasSuffix(base, ".csproj") || strings.HasSuffix(base, ".fsproj") || strings.HasSuffix(base, ".vbproj") || strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt")
}

func (e *Engine) dependencyInputs(ctx context.Context, opened *lane) (map[string]string, error) {
	inputs := make(map[string]string)
	names := map[string]bool{}
	for name := range opened.snapshot.Baseline.Files {
		names[name] = true
	}
	candidates, err := e.candidates(ctx, opened)
	if err != nil {
		return nil, err
	}
	for _, name := range candidates {
		names[name] = true
	}
	for name := range names {
		baseline := opened.snapshot.Baseline.Files[name]
		if !dependencyFile(name) {
			continue
		}
		current, _, err := state.ReadVersion(opened.root, name, baseline, opened.snapshot.Manifest.RespectExecutableBits)
		if err != nil {
			return nil, err
		}
		if !current.Exists() {
			inputs[name] = "missing"
		} else {
			inputs[name] = current.Hash
		}
	}
	return inputs, nil
}

func (e *Engine) SharedWarnings(ctx context.Context, name string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opened, err := e.openLane(name)
	if err != nil {
		return nil, err
	}
	defer opened.close()
	if len(opened.snapshot.Manifest.Mappings) == 0 {
		return nil, nil
	}
	inputs, err := e.dependencyInputs(ctx, opened)
	if err != nil {
		return nil, err
	}
	warnings := []string{}
	for _, mapping := range opened.snapshot.Manifest.Mappings {
		actualTarget, linkErr := opened.root.Readlink(mapping.Path)
		if linkErr != nil || actualTarget != mapping.Target {
			warnings = append(warnings, "Dependency mapping "+mapping.Path+" is missing, inaccessible or retargeted; repair or unshare it before building.")
		}
		changed := mapping.Platform != runtime.GOOS+"/"+runtime.GOARCH || len(mapping.Inputs) != len(inputs)
		for input, hash := range inputs {
			if mapping.Inputs[input] != hash {
				changed = true
			}
		}
		if changed {
			warnings = append(warnings, "Dependency mapping "+mapping.Path+" is stale: manifest/lockfile inputs or platform changed. Detach it and explicitly prepare a compatible environment before building.")
		}
	}
	return warnings, nil
}
