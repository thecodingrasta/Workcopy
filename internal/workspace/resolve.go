package workspace

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"

	"workcopy/internal/fsops"
	"workcopy/internal/git"
	"workcopy/internal/model"
	"workcopy/internal/state"
)

type ResolveOptions struct {
	UseLocal, UseUpstream, Delete             bool
	FromFile, ExpectedConflict, ExpectedLocal string
	// FromLaneFile confines FromFile to the verified lane root (MCP callers).
	FromLaneFile bool
}

func versionBody(reader *git.Reader, store *state.Store, version model.FileVersion) ([]byte, error) {
	if !version.Exists() {
		return nil, nil
	}
	var body []byte
	var err error
	if version.ObjectID != "" {
		body, err = reader.Read(version.ObjectID)
	} else {
		body, err = store.Blob(version.Hash)
	}
	if err != nil {
		return nil, err
	}
	if model.Hash(body) != version.Hash || int64(len(body)) != version.Size {
		return nil, fmt.Errorf("snapshot content hash mismatch")
	}
	return body, nil
}
func (e *Engine) Resolve(ctx context.Context, name, relative string, options ResolveOptions, dryRun bool) (Report, error) {
	report := Report{Command: "resolve"}
	if err := fsops.ValidateSourcePath(relative); err != nil {
		return report, err
	}
	choices := 0
	for _, choice := range []bool{options.UseLocal, options.UseUpstream, options.Delete, options.FromFile != ""} {
		if choice {
			choices++
		}
	}
	if choices != 1 || options.ExpectedConflict == "" {
		return report, model.Fail(2, "resolution-choice-required", "Provide --expect-conflict and exactly one of --use-local, --use-upstream, --delete or --from-file")
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
	for n, c := range opened.snapshot.Conflicts {
		if c.Path == relative && c.ID == options.ExpectedConflict {
			index = n
			break
		}
	}
	if index < 0 {
		return report, model.Fail(3, "stale-conflict", "Conflict path/token no longer matches the recorded conflict")
	}
	conflict := opened.snapshot.Conflicts[index]
	current, currentBody, err := state.ReadVersion(opened.root, relative, conflict.Local, opened.snapshot.Manifest.RespectExecutableBits)
	if err != nil {
		return report, err
	}
	if !current.Equal(conflict.Local) && (options.ExpectedLocal == "" || options.ExpectedLocal != current.Hash) {
		return report, model.Fail(3, "local-content-changed", "Local content changed after the conflict was recorded. Supply --expect-local with its current SHA-256 to accept that exact state")
	}
	reader, err := e.Repository.Reader(ctx)
	if err != nil {
		return report, err
	}
	defer reader.Close()
	desired, body := current, currentBody
	if options.UseUpstream {
		desired = conflict.Upstream
		body, err = versionBody(reader, opened.store, desired)
		if err != nil {
			return report, err
		}
	}
	if options.Delete {
		desired = model.FileVersion{Kind: model.Missing}
		body = nil
	}
	if options.FromFile != "" {
		if options.FromLaneFile {
			body, err = opened.root.ReadFile(options.FromFile)
		} else {
			absolute, pathErr := filepath.Abs(options.FromFile)
			if pathErr != nil {
				return report, pathErr
			}
			source, openErr := fsops.OpenRoot(filepath.Dir(absolute))
			if openErr != nil {
				return report, openErr
			}
			body, err = source.ReadFile(filepath.Base(absolute))
			source.Close()
		}
		if err != nil {
			return report, err
		}
		mode := uint32(0644)
		if current.Kind == model.Regular {
			mode = current.Mode
		}
		desired = model.Version(model.Regular, mode, body)
	}
	if desired.Kind == model.Symlink {
		if err = validateLink(relative, string(body)); err != nil {
			return report, err
		}
	}
	report.Plan = &Plan{Action: "resolve", Files: []string{relative}, Effects: []string{"Accept the recorded upstream version as baseline", "Keep any difference in the chosen resolution as a local contribution"}}
	if dryRun {
		report.Message = "Resolution validated without changing source or metadata."
		return report, nil
	}
	next := newGeneration(opened.snapshot)
	next.Baseline.Files[relative] = conflict.Upstream
	next.Conflicts = append(next.Conflicts[:index], next.Conflicts[index+1:]...)
	if len(next.Conflicts) == 0 {
		next.Manifest.SyncedCommit = next.Manifest.PendingCommit
		next.Manifest.PendingCommit = ""
	}
	tx, err := opened.store.Begin(next)
	if err != nil {
		return report, err
	}
	if !current.Equal(desired) {
		if err = tx.Add(relative, current, desired, body, false); err != nil {
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
	report.Conflicts = next.Conflicts
	report.Message = "Resolution accepted. Differences from upstream remain local contributions."
	if len(next.Conflicts) > 0 {
		report.ExitCode = 1
	}
	return report, nil
}
func (e *Engine) Content(ctx context.Context, name, token, side string, offset, limit int64) (Report, error) {
	report := Report{Command: "content"}
	if offset < 0 || limit < 1 || limit > 65536 {
		return report, model.Fail(2, "invalid-content-range", "Offset must be nonnegative; limit must be 1-65536 bytes")
	}
	opened, err := e.openLane(name)
	if err != nil {
		return report, err
	}
	defer opened.close()
	lock, err := e.reloadLocked(opened)
	if err != nil {
		return report, err
	}
	defer lock.Close()
	var conflict *model.Conflict
	for n := range opened.snapshot.Conflicts {
		if opened.snapshot.Conflicts[n].ID == token {
			conflict = &opened.snapshot.Conflicts[n]
			break
		}
	}
	if conflict == nil {
		return report, model.Fail(3, "stale-conflict", "Conflict token was not found")
	}
	var version model.FileVersion
	switch side {
	case "base":
		version = conflict.Base
	case "local":
		version = conflict.Local
	case "upstream":
		version = conflict.Upstream
	default:
		return report, model.Fail(2, "invalid-content-side", "Side must be base, local or upstream")
	}
	reader, err := e.Repository.Reader(ctx)
	if err != nil {
		return report, err
	}
	defer reader.Close()
	body, err := versionBody(reader, opened.store, version)
	if err != nil {
		return report, err
	}
	total := int64(len(body))
	if offset > total {
		return report, model.Fail(2, "invalid-content-range", "Offset exceeds content length")
	}
	end := offset + limit
	if end > total {
		end = total
	}
	next := end
	if end == total {
		next = -1
	}
	report.Content = &Content{Side: side, Encoding: "base64", Data: base64.StdEncoding.EncodeToString(body[offset:end]), Offset: offset, Total: total, NextOffset: next, SHA256: version.Hash}
	return report, nil
}
