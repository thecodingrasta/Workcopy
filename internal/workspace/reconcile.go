package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"workcopy/internal/model"
	"workcopy/internal/state"
)

func (e *Engine) mergeText(ctx context.Context, base, local, upstream []byte) ([]byte, bool, error) {
	if bytes.ContainsRune(base, 0) || bytes.ContainsRune(local, 0) || bytes.ContainsRune(upstream, 0) {
		return nil, false, nil
	}
	directory, err := os.MkdirTemp("", "workcopy-merge-")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(directory)
	for name, body := range map[string][]byte{"base": base, "local": local, "upstream": upstream} {
		if err = os.WriteFile(filepath.Join(directory, name), body, 0600); err != nil {
			return nil, false, err
		}
	}
	command := e.Repository.Command(ctx, directory, "merge-file", "--stdout", "--diff3", "-L", "local", "-L", "base", "-L", "upstream", "--", "local", "base", "upstream")
	var output, diagnostics bytes.Buffer
	command.Stdout = &output
	command.Stderr = &diagnostics
	if err = command.Run(); err != nil {
		var exit *exec.ExitError
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		if ok := errors.As(err, &exit); ok && exit.ExitCode() > 0 && exit.ExitCode() <= 127 {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("git merge-file: %w: %s", err, diagnostics.String())
	}
	if int64(output.Len()) > model.MaxFileBytes {
		return nil, false, fmt.Errorf("merged file exceeds 64 MiB")
	}
	return output.Bytes(), true, nil
}
func (e *Engine) Sync(ctx context.Context, name string, dryRun bool) (Report, error) {
	report := Report{Command: "sync"}
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
	report.Workspace = view(opened.snapshot)
	report.Generation = opened.snapshot.Manifest.Generation
	if len(opened.snapshot.Conflicts) > 0 {
		report.ExitCode = 1
		report.Conflicts = opened.snapshot.Conflicts
		report.Message = "Resolve the recorded conflicts before syncing a newer snapshot."
		return report, nil
	}
	_, commit, err := e.Repository.Resolve(ctx, opened.snapshot.Manifest.Branch)
	if err != nil {
		return report, err
	}
	if commit == opened.snapshot.Manifest.SyncedCommit {
		report.ExitCode = 4
		report.Message = "The selected upstream snapshot has not changed."
		return report, nil
	}
	tree, err := e.Repository.Tree(ctx, commit)
	if err != nil {
		return report, err
	}
	names, err := validateTree(tree, opened.snapshot.Manifest.Selection, false)
	if err != nil {
		return report, err
	}
	if err = e.validateAttributes(ctx, commit, names); err != nil {
		return report, err
	}
	current, err := e.scan(ctx, opened)
	if err != nil {
		return report, err
	}
	keys := map[string]bool{}
	for name := range opened.snapshot.Baseline.Files {
		keys[name] = true
	}
	for name := range current {
		keys[name] = true
	}
	for _, name := range names {
		keys[name] = true
	}
	names = names[:0]
	for filename := range keys {
		for _, mapping := range opened.snapshot.Manifest.Mappings {
			if filename == mapping.Path || strings.HasPrefix(filename, mapping.Path+"/") {
				return report, model.Fail(3, "mapping-overlaps-source", "Upstream source overlaps a shared dependency mapping; unshare it before syncing")
			}
		}
		names = append(names, filename)
	}
	sort.Strings(names)
	report.Plan = &Plan{Action: "sync", Files: names, Effects: []string{"Reconcile the pinned upstream snapshot against each accepted baseline", "Preserve local contributions and record unresolved conflicts"}}
	if dryRun {
		report.Message = "Sync candidates inspected without writes. Text-merge outcomes are evaluated during apply."
		return report, nil
	}
	reader, err := e.Repository.Reader(ctx)
	if err != nil {
		return report, err
	}
	defer reader.Close()
	next := newGeneration(opened.snapshot)
	type edit struct {
		name          string
		before, after model.FileVersion
	}
	edits := []edit{}
	for _, filename := range names {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		base := opened.snapshot.Baseline.Files[filename]
		local, localBody, readErr := state.ReadVersion(opened.root, filename, base, opened.snapshot.Manifest.RespectExecutableBits)
		if readErr != nil {
			return report, readErr
		}
		up := model.FileVersion{Kind: model.Missing}
		var upBody []byte
		if entry, ok := tree[filename]; ok {
			if base.ObjectID == entry.ObjectID && base.Exists() {
				up = base
			} else {
				upBody, readErr = reader.Read(entry.ObjectID)
				if readErr != nil {
					return report, readErr
				}
				up, readErr = entryVersion(entry, upBody)
				if readErr != nil {
					return report, readErr
				}
			}
		}
		desired, body := local, localBody
		conflict := false
		reason := "both sides changed"
		switch {
		case up.Equal(base):
			continue
		case local.Equal(base):
			desired = up
			body = upBody
			if up.Exists() && body == nil {
				body, err = reader.Read(up.ObjectID)
				if err != nil {
					return report, err
				}
			}
		case local.Equal(up):
		case base.Kind == model.Regular && local.Kind == model.Regular && up.Kind == model.Regular:
			baseBody, baseErr := reader.Read(base.ObjectID)
			if baseErr != nil {
				return report, baseErr
			}
			if upBody == nil {
				upBody, err = reader.Read(up.ObjectID)
				if err != nil {
					return report, err
				}
			}
			merged, clean, mergeErr := e.mergeText(ctx, baseBody, localBody, upBody)
			if mergeErr != nil {
				return report, mergeErr
			}
			if !clean {
				conflict = true
				reason = "overlapping text edits or binary content"
			} else {
				mode := local.Mode
				if local.Mode == base.Mode {
					mode = up.Mode
				}
				desired = model.Version(model.Regular, mode, merged)
				body = merged
			}
		default:
			conflict = true
			reason = "add/delete or file-type conflict"
		}
		if conflict {
			token, idErr := model.ID()
			if idErr != nil {
				return report, idErr
			}
			if local.Exists() {
				if _, err = opened.store.PutBlob(localBody); err != nil {
					return report, err
				}
			}
			next.Conflicts = append(next.Conflicts, model.Conflict{ID: token, Path: filename, Reason: reason, Base: base, Local: local, Upstream: up, UpstreamCommit: commit})
			continue
		}
		next.Baseline.Files[filename] = up
		if !local.Equal(desired) {
			if desired.Exists() {
				if _, err = opened.store.PutBlob(body); err != nil {
					return report, err
				}
			}
			edits = append(edits, edit{filename, local, desired})
		}
	}
	if len(next.Conflicts) > 0 {
		next.Manifest.PendingCommit = commit
	} else {
		next.Manifest.SyncedCommit = commit
		next.Manifest.PendingCommit = ""
	}
	if err = e.Repository.Pin(ctx, next.Manifest.ID, commit); err != nil {
		return report, err
	}
	tx, err := opened.store.Begin(next)
	if err != nil {
		return report, err
	}
	for _, edit := range edits {
		if err = tx.AddStaged(edit.name, edit.before, edit.after, false); err != nil {
			return report, err
		}
	}
	if err = tx.Prepare(); err != nil {
		return report, err
	}
	if err = tx.Apply(); err != nil {
		return report, err
	}
	opened.snapshot = next
	current, err = e.scan(ctx, opened)
	if err != nil {
		return report, err
	}
	report.Workspace = view(next)
	report.Generation = next.Manifest.Generation
	report.Changes = changes(next.Baseline.Files, current)
	report.Conflicts = next.Conflicts
	report.Message = "Upstream reconciled. Local contributions remain pending until reviewed and integrated."
	if len(next.Conflicts) > 0 {
		report.ExitCode = 1
		report.Message = "Conflicts were recorded. Nonconflicting updates were applied; resolve each conflict explicitly."
	}
	return report, nil
}
