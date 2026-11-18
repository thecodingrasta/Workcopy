package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"workcopy/internal/git"
	"workcopy/internal/model"
	"workcopy/internal/state"
)

// batchInput binds the batch to the exact contribution, even if an editor
// changes a lane without advancing its manifest generation.
type batchInput struct {
	Name       string `json:"name"`
	ID         string `json:"id"`
	Generation uint64 `json:"generation"`
	Digest     string `json:"digest"`
}

type batchDelta struct {
	Path  string
	Base  model.FileVersion
	Local model.FileVersion
}

func (e *Engine) inspectBatchInput(ctx context.Context, opened *lane) (batchInput, []batchDelta, error) {
	m := opened.snapshot.Manifest
	input := batchInput{Name: m.Name, ID: m.ID, Generation: m.Generation}
	if len(opened.snapshot.Conflicts) != 0 || m.PendingCommit != "" {
		return input, nil, model.Fail(1, "batch-lane-conflicts", "Resolve recorded conflicts in lane "+m.Name+" before preparing the batch")
	}
	current, err := e.scan(ctx, opened)
	if err != nil {
		return input, nil, err
	}
	base := opened.snapshot.Baseline.Files
	delta := []batchDelta{}
	for _, change := range changes(base, current) {
		delta = append(delta, batchDelta{change.Path, base[change.Path], current[change.Path]})
	}
	encoded, err := json.Marshal(delta)
	if err != nil {
		return input, nil, err
	}
	input.Digest = model.Hash(encoded)
	return input, delta, nil
}

type batchTree struct {
	target      map[string]git.TreeEntry
	tree        map[string]git.TreeEntry
	reader      *git.Reader
	base        string
	resolutions map[string]batchResolution
}

type BatchResolution struct {
	Token   string  `json:"token"`
	Choice  string  `json:"choice"`
	Content *string `json:"content,omitempty"`
}

type batchResolution struct {
	choice  string
	content []byte
}

type BatchConflict struct {
	Token    string            `json:"token"`
	Lane     string            `json:"lane"`
	Path     string            `json:"path"`
	Base     model.FileVersion `json:"base"`
	Local    model.FileVersion `json:"local"`
	Combined model.FileVersion `json:"combined"`
}

func (c *BatchConflict) Error() string {
	return "Resolve " + c.Lane + "/" + c.Path + " against the combined batch result using token " + c.Token
}

func (c *BatchConflict) Unwrap() error { return model.Fail(1, "batch-conflict", c.Error()) }

func batchResolutions(items []BatchResolution) (map[string]batchResolution, error) {
	result := make(map[string]batchResolution, len(items))
	var total int64
	if len(items) > 1000 {
		return nil, model.Fail(2, "batch-resolution-limit", "Too many batch resolutions")
	}
	for _, item := range items {
		if len(item.Token) != 64 || result[item.Token].choice != "" {
			return nil, model.Fail(2, "invalid-batch-resolution", "Use each reported conflict token once")
		}
		resolution := batchResolution{choice: item.Choice}
		switch item.Choice {
		case "lane", "combined", "delete":
			if item.Content != nil {
				return nil, model.Fail(2, "invalid-batch-resolution", "Only the file resolution accepts content")
			}
		case "file":
			if item.Content == nil {
				return nil, model.Fail(2, "invalid-batch-resolution", "File resolution requires base64 content (empty is allowed)")
			}
			total += int64(len(*item.Content))
			if total > model.MaxMetadataBytes {
				return nil, model.Fail(2, "batch-resolution-limit", "Batch resolution data exceeds the configured size limit")
			}
			decoded, err := base64.StdEncoding.DecodeString(*item.Content)
			if err != nil {
				return nil, model.Fail(2, "invalid-batch-resolution", "File resolution content must be valid base64")
			}
			resolution.content = decoded
		default:
			return nil, model.Fail(2, "invalid-batch-resolution", "Resolution choice must be lane, combined, delete or file")
		}
		result[item.Token] = resolution
	}
	return result, nil
}

func (e *Engine) applyBatchDelta(ctx context.Context, opened *lane, tree *batchTree, edit batchDelta) error {
	var upstream model.FileVersion
	var upstreamBody []byte
	entry, exists := tree.tree[edit.Path]
	if exists {
		var err error
		upstream, upstreamBody, err = entryVersionFrom(tree.reader, entry, "")
		if err != nil {
			return err
		}
	}
	if edit.Local.Equal(upstream) {
		return nil
	}
	local, body, err := state.ReadVersion(opened.root, edit.Path, opened.baselineFor(edit.Path), opened.snapshot.Manifest.RespectExecutableBits)
	if err != nil {
		return err
	}
	if !local.Equal(edit.Local) {
		return model.Fail(3, "batch-input-changed", "Lane changed during batch preparation: "+opened.record.Name+"/"+edit.Path)
	}
	if !upstream.Equal(edit.Base) {
		clean := false
		if edit.Base.Kind == model.Regular && local.Kind == model.Regular && upstream.Kind == model.Regular &&
			!edit.Base.Large() && !local.Large() && !upstream.Large() {
			baseBody, readErr := tree.reader.Read(edit.Base.ObjectID)
			if readErr != nil {
				return readErr
			}
			var merged []byte
			merged, clean, err = e.mergeText(ctx, baseBody, body, upstreamBody)
			if err != nil {
				return err
			}
			if clean {
				mode := local.Mode
				if mode == edit.Base.Mode {
					mode = upstream.Mode
				}
				body, local = merged, model.Version(model.Regular, mode, merged)
			}
		}
		if !clean {
			conflict := BatchConflict{Lane: opened.record.Name, Path: edit.Path, Base: edit.Base, Local: edit.Local, Combined: upstream}
			tokenData, encodeErr := json.Marshal(conflict)
			if encodeErr != nil {
				return encodeErr
			}
			conflict.Token = model.Hash(append([]byte(tree.base+opened.record.ID), tokenData...))
			resolution, ok := tree.resolutions[conflict.Token]
			if !ok {
				return &conflict
			}
			delete(tree.resolutions, conflict.Token)
			switch resolution.choice {
			case "combined":
				return nil
			case "delete":
				local = model.FileVersion{Kind: model.Missing}
			case "file":
				if local.Kind != model.Regular && upstream.Kind != model.Regular {
					return model.Fail(2, "invalid-batch-resolution", "File resolution requires a regular file on one side")
				}
				mode := local.Mode
				if local.Kind != model.Regular {
					mode = upstream.Mode
				}
				body = resolution.content
				local = model.Version(model.Regular, mode, body)
			}
		}
	}
	if !local.Exists() {
		delete(tree.tree, edit.Path)
		return nil
	}
	mode := uint32(0100644)
	switch {
	case local.Kind == model.Symlink:
		if err = validateLink(edit.Path, string(body)); err != nil {
			return err
		}
		mode = 0120000
	case local.Mode == 0755:
		mode = 0100755
	}
	var objectID string
	if local.Large() && body == nil {
		objectID, err = e.hashLargeReviewFile(ctx, opened.root, e.Repository.Root, edit.Path, local)
	} else {
		var oid []byte
		oid, err = e.Repository.Run(ctx, e.Repository.Root, body, "hash-object", "-w", "--no-filters", "--stdin")
		objectID = strings.TrimSpace(string(oid))
	}
	if err != nil {
		return err
	}
	if !git.ValidObjectID(objectID) {
		return fmt.Errorf("invalid batch blob ID")
	}
	tree.tree[edit.Path] = git.TreeEntry{Mode: mode, ObjectID: objectID, Size: local.Size, Kind: "blob"}
	return nil
}

func (tree *batchTree) indexEdits() []byte {
	names := []string{}
	for path := range tree.target {
		if tree.tree[path] != tree.target[path] {
			names = append(names, path)
		}
	}
	for path := range tree.tree {
		if _, exists := tree.target[path]; !exists {
			names = append(names, path)
		}
	}
	sort.Strings(names)
	var edits bytes.Buffer
	// Remove first, so a directory can become a file (and vice versa).
	for _, path := range names {
		if _, exists := tree.tree[path]; !exists {
			fmt.Fprintf(&edits, "0 %s\t%s\x00", strings.Repeat("0", len(tree.target[path].ObjectID)), path)
		}
	}
	for _, path := range names {
		if entry, exists := tree.tree[path]; exists {
			fmt.Fprintf(&edits, "%o %s\t%s\x00", entry.Mode, entry.ObjectID, path)
		}
	}
	return edits.Bytes()
}

func (tree *batchTree) verifyShape() error {
	for path := range tree.tree {
		for last := strings.LastIndexByte(path, '/'); last >= 0; last = strings.LastIndexByte(path[:last], '/') {
			if _, exists := tree.tree[path[:last]]; exists {
				return model.Fail(1, "batch-path-conflict", "Selected contributions use a path as both a file and a directory: "+path[:last])
			}
		}
	}
	return nil
}
