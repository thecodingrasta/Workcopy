package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
	"workcopy/internal/state"
)

// A lane directory deleted outside Workcopy leaves a registration, Git pins and
// possibly review worktrees behind. The helpers here recognise that state
// ("registered but absent") so every lane command can report it as one named
// condition and destroy can clear it without touching anything else.

func (e *Engine) laneDirectoryGone(record registration) (bool, error) {
	_, err := os.Lstat(record.Path)
	if os.IsNotExist(err) {
		return true, nil
	}
	return false, err
}

func (e *Engine) registrationFile(name string) string {
	return filepath.Join(e.WorkRoot, filepath.FromSlash(registrationPath(name)))
}

func (e *Engine) missingFailure(record registration) error {
	failure := model.Fail(3, "workspace-missing", fmt.Sprintf("Lane %q is registered but its directory no longer exists: %s (registration: %s)", record.Name, record.Path, e.registrationFile(record.Name))).(*model.Failure)
	failure.Suggestion = fmt.Sprintf("Run `workcopy destroy %s` to clear the registration. Review worktrees and shared dependency targets are not deleted.", record.Name)
	return failure
}

// missingView describes a missing lane for status output; it returns nil for
// any other error so unrelated failures are not relabelled.
func (e *Engine) missingView(name string, cause error) *WorkspaceView {
	var failure *model.Failure
	if !errors.As(cause, &failure) || failure.Code != "workspace-missing" {
		return nil
	}
	record, err := e.readRegistration(name)
	if err != nil {
		return nil
	}
	return &WorkspaceView{ID: record.ID, Name: record.Name, Path: record.Path, Branch: e.registeredBranch(record), Phase: "missing"}
}

// registeredBranch prefers the value cached in the registration and falls back
// to the manifest for lanes created before the cache existed.
func (e *Engine) registeredBranch(record registration) string {
	if record.Branch != "" {
		return record.Branch
	}
	if record.Phase != "ready" {
		return ""
	}
	root, err := fsops.OpenRoot(record.Path)
	if err != nil {
		return ""
	}
	defer root.Close()
	snapshot, err := state.New(root).Load()
	if err != nil {
		return ""
	}
	return snapshot.Manifest.Branch
}

func (e *Engine) listView(record registration) WorkspaceView {
	view := WorkspaceView{ID: record.ID, Name: record.Name, Path: record.Path, Phase: record.Phase}
	if record.Phase == "ready" {
		if gone, err := e.laneDirectoryGone(record); err == nil && gone {
			view.Phase = "missing"
		}
	}
	if view.Phase != "missing" {
		view.Branch = e.registeredBranch(record)
	} else {
		view.Branch = record.Branch
	}
	return view
}

// reviewDirectoryName matches the directory of a review of this lane: the lane
// name, a hyphen and the first 8 to 32 hex digits of the review's ID. Reviews
// made before the short form existed use all 32 digits and stay valid.
func reviewDirectoryName(lane string) *regexp.Regexp {
	return regexp.MustCompile("^" + regexp.QuoteMeta(lane) + "-[0-9a-f]{8,32}$")
}

// pathKey makes registered-worktree comparison case-insensitive where the
// filesystem is, without altering the paths shown to the user.
func pathKey(path string) string {
	path = filepath.Clean(filepath.FromSlash(path))
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

type orphanReview struct {
	Path       string
	Registered bool
}

// registeredWorktrees returns the Git-registered worktree paths of the source
// repository, keyed by pathKey. A listing failure yields an empty set, which
// only downgrades the advice from "git worktree remove" to "delete manually".
func (e *Engine) registeredWorktrees(ctx context.Context) map[string]bool {
	registered := map[string]bool{}
	if body, err := e.Repository.Run(ctx, e.Repository.Root, nil, "worktree", "list", "--porcelain"); err == nil {
		for _, line := range strings.Split(string(body), "\n") {
			if path, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree "); ok {
				registered[pathKey(path)] = true
			}
		}
	}
	return registered
}

// retainedReviews lists review worktrees that belonged to the lane. They are
// never deleted: they may hold the only copy of work the lane produced.
func (e *Engine) retainedReviews(ctx context.Context, lane string) ([]orphanReview, error) {
	directory := filepath.Join(e.WorkRoot, ".workcopy-reviews")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pattern := reviewDirectoryName(lane)
	registered := e.registeredWorktrees(ctx)
	reviews := []orphanReview{}
	for _, entry := range entries {
		if !entry.IsDir() || !pattern.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		reviews = append(reviews, orphanReview{Path: path, Registered: registered[pathKey(path)]})
	}
	sort.Slice(reviews, func(i, j int) bool { return reviews[i].Path < reviews[j].Path })
	return reviews, nil
}

// adviseRetainedReviews tells the user exactly how to remove what Workcopy
// intentionally left behind. The commands are display only.
func (e *Engine) adviseRetainedReviews(report *Report, reviews []orphanReview) {
	for _, review := range reviews {
		if review.Registered {
			report.Warnings = append(report.Warnings, "Review worktree retained: "+review.Path)
			report.NextCommands = append(report.NextCommands, fmt.Sprintf("git -C %s worktree remove --force %s   # discards that review's uncommitted content", shellQuote(e.Repository.Root), shellQuote(review.Path)))
		} else {
			report.Warnings = append(report.Warnings, "Review directory retained (not a registered Git worktree; delete it manually if unneeded): "+review.Path)
		}
	}
}
func (e *Engine) destroyMissing(ctx context.Context, record registration, dryRun bool) (Report, error) {
	report := Report{Command: "destroy"}
	report.Workspace = &WorkspaceView{ID: record.ID, Name: record.Name, Path: record.Path, Branch: e.registeredBranch(record), Phase: "missing"}
	report.Plan = &Plan{Action: "clear-missing-lane", Files: []string{record.Name}, Effects: []string{
		"Remove the registration " + e.registrationFile(record.Name),
		"Remove Workcopy's refs/workcopy pins for this lane",
		"Delete nothing inside or outside the lane; review worktrees and shared dependency targets are kept",
	}}
	reviews, err := e.retainedReviews(ctx, record.Name)
	if err != nil {
		return report, err
	}
	e.adviseRetainedReviews(&report, reviews)
	if dryRun {
		report.Message = "Missing lane would be cleared; no registry, ref or filesystem changes were made."
		return report, nil
	}
	registry, lock, err := e.rootLock()
	if err != nil {
		return report, err
	}
	defer registry.Close()
	defer lock.Close()
	// Revalidate under the registry lock: the directory may have reappeared (a
	// restore from backup) or another process may have cleared the entry.
	fresh, err := e.readRegistration(record.Name)
	if err != nil {
		return report, err
	}
	if fresh.ID != record.ID || fresh.Phase != "ready" {
		return report, model.Fail(3, "registration-changed", "The registration changed while clearing; nothing was removed")
	}
	if gone, goneErr := e.laneDirectoryGone(fresh); goneErr != nil {
		return report, goneErr
	} else if !gone {
		return report, model.Fail(3, "registration-changed", "The lane directory reappeared; nothing was removed")
	}
	if _, statErr := os.Lstat(filepath.Join(e.WorkRoot, ".workcopy-trash-"+fresh.ID)); statErr == nil {
		return report, model.Fail(3, "deletion-path-collision", "A quarantine directory exists for this lane; nothing was removed")
	} else if !os.IsNotExist(statErr) {
		return report, statErr
	}
	if err = e.Repository.Unpin(ctx, fresh.ID); err != nil {
		return report, err
	}
	if err = registry.Remove(registrationPath(fresh.Name)); err != nil {
		return report, err
	}
	report.Workspace.Phase = "cleared"
	report.Message = "Missing lane cleared: registration and Workcopy pins removed. Nothing else was deleted."
	return report, nil
}

// shellQuote renders a path for a command the user will paste. It is display
// only: Workcopy never executes these strings.
func shellQuote(path string) string {
	if runtime.GOOS == "windows" {
		return `"` + path + `"`
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}
