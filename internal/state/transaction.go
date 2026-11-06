package state

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"workcopy/internal/eol"
	"workcopy/internal/fsops"
	"workcopy/internal/model"
)

type Operation struct {
	Path         string            `json:"path"`
	Before       model.FileVersion `json:"before"`
	After        model.FileVersion `json:"after"`
	ExternalLink bool              `json:"externalLink,omitempty"`
}
type Journal struct {
	SchemaVersion  int            `json:"schemaVersion"`
	WorkspaceID    string         `json:"workspaceId"`
	FromGeneration uint64         `json:"fromGeneration"`
	Next           model.Snapshot `json:"next"`
	Operations     []Operation    `json:"operations"`
}
type Transaction struct {
	store    *Store
	journal  Journal
	prepared bool
	paths    map[string]bool
}

func (s *Store) Begin(next model.Snapshot) (*Transaction, error) {
	if _, err := s.Root.Lstat(JournalPath); err == nil {
		return nil, model.Fail(3, "recovery-required", "An unfinished transaction exists; run recover")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	current, err := s.Load()
	if err != nil {
		return nil, err
	}
	if next.Manifest.Generation != current.Manifest.Generation+1 || next.Manifest.ID != current.Manifest.ID {
		return nil, fmt.Errorf("invalid state generation")
	}
	return &Transaction{store: s, paths: map[string]bool{}, journal: Journal{SchemaVersion: 1, WorkspaceID: current.Manifest.ID, FromGeneration: current.Manifest.Generation, Next: next}}, nil
}
func (t *Transaction) canQueue(name string) error {
	if t.prepared {
		return fmt.Errorf("transaction already prepared")
	}
	if err := fsops.ValidateSourcePath(name); err != nil {
		return err
	}
	if t.paths[name] {
		return fmt.Errorf("duplicate transaction path")
	}
	return nil
}
func (t *Transaction) Add(name string, before, after model.FileVersion, body []byte, external bool) error {
	if err := t.canQueue(name); err != nil {
		return err
	}
	if after.Exists() {
		if model.Hash(body) != after.Hash || int64(len(body)) != after.Size {
			return fmt.Errorf("operation content does not match its hash")
		}
		if _, err := t.store.PutBlob(body); err != nil {
			return err
		}
	}
	return t.AddStaged(name, before, after, external)
}

// AddFromObject queues a write whose content is the Git object recorded on
// after. Nothing is staged: the content is read from Git when the transaction
// is applied or replayed, and verified against its recorded hash then.
func (t *Transaction) AddFromObject(name string, before, after model.FileVersion) error {
	if t.store.Objects == nil || after.ObjectID == "" || !after.Exists() {
		return fmt.Errorf("no Git object source for %s", name)
	}
	return t.AddStaged(name, before, after, false)
}

// sourced reports whether an operation's content comes from Git.
func (t *Transaction) sourced(version model.FileVersion) bool {
	return t.store.Objects != nil && version.ObjectID != "" && version.Exists()
}

// afterBody returns the bytes an operation writes, verified against its hash.
func (t *Transaction) afterBody(version model.FileVersion) ([]byte, error) {
	if t.sourced(version) {
		body, err := t.store.Objects.Read(version.ObjectID)
		if err == nil && model.Hash(body) == version.Hash && int64(len(body)) == version.Size {
			return body, nil
		}
	}
	return t.store.Blob(version.Hash)
}

// AddStaged avoids retaining or re-reading payloads already staged by planning.
// Prepare verifies every referenced object before persisting a journal.
func (t *Transaction) AddStaged(name string, before, after model.FileVersion, external bool) error {
	if err := t.canQueue(name); err != nil {
		return err
	}
	if err := validateVersion(before); err != nil {
		return err
	}
	if err := validateVersion(after); err != nil {
		return err
	}
	t.paths[name] = true
	t.journal.Operations = append(t.journal.Operations, Operation{name, before, after, external})
	return nil
}
func (t *Transaction) Prepare() error {
	sort.Slice(t.journal.Operations, func(i, j int) bool {
		a, b := t.journal.Operations[i], t.journal.Operations[j]
		if a.After.Exists() != b.After.Exists() {
			return !a.After.Exists()
		}
		return a.Path < b.Path
	})
	if err := t.preflight(false); err != nil {
		return err
	}
	body, err := json.MarshalIndent(t.journal, "", "  ")
	if err != nil {
		return err
	}
	if int64(len(body)) > model.MaxMetadataBytes {
		return fmt.Errorf("journal exceeds metadata limit")
	}
	if err = t.store.Root.WriteAtomic(JournalPath, body, 0600); err != nil {
		return err
	}
	t.prepared = true
	return nil
}
func (t *Transaction) preflight(replay bool) error {
	if err := validateSnapshot(t.journal.Next); err != nil {
		return err
	}
	if t.journal.WorkspaceID != t.journal.Next.Manifest.ID {
		return fmt.Errorf("journal changes workspace identity")
	}
	seen := map[string]bool{}
	for _, op := range t.journal.Operations {
		if seen[op.Path] {
			return fmt.Errorf("duplicate journal path")
		}
		seen[op.Path] = true
		if err := validateVersion(op.Before); err != nil {
			return err
		}
		if err := validateVersion(op.After); err != nil {
			return err
		}
	}
	snapshot, err := t.store.Load()
	if err != nil {
		return err
	}
	if snapshot.Manifest.Name != t.journal.Next.Manifest.Name || snapshot.Manifest.WorkspacePath != t.journal.Next.Manifest.WorkspacePath || snapshot.Manifest.CommonGitDir != t.journal.Next.Manifest.CommonGitDir || snapshot.Manifest.WorkRoot != t.journal.Next.Manifest.WorkRoot || snapshot.Manifest.RepositoryRoot != t.journal.Next.Manifest.RepositoryRoot {
		return fmt.Errorf("journal changes workspace ownership")
	}
	if snapshot.Manifest.ID != t.journal.WorkspaceID {
		return fmt.Errorf("journal workspace identity mismatch")
	}
	if snapshot.Manifest.Generation != t.journal.FromGeneration && (!replay || snapshot.Manifest.Generation != t.journal.Next.Manifest.Generation) {
		return model.Fail(3, "stale-journal", "Workspace generation changed")
	}
	respect := t.store.RespectExecutableBits(t.journal.Next.Manifest.RespectExecutableBits)
	operations := make(map[string]Operation, len(t.journal.Operations))
	for _, op := range t.journal.Operations {
		operations[op.Path] = op
	}
	// Each operation is checked independently and read-only, so the checks
	// overlap: on slow filesystems this is dominated by per-file latency.
	ctx := context.Background()
	writes := make([]Operation, 0, len(t.journal.Operations))
	for _, op := range t.journal.Operations {
		if op.After.Exists() {
			writes = append(writes, op)
		}
	}
	if err = fsops.Parallel(ctx, len(writes), func(index int) error {
		return t.validateWriteShape(writes[index], operations)
	}); err != nil {
		return err
	}
	return fsops.Parallel(ctx, len(t.journal.Operations), func(index int) error {
		op := t.journal.Operations[index]
		if err := fsops.ValidateSourcePath(op.Path); err != nil {
			return err
		}
		if op.After.Exists() && !t.sourced(op.After) {
			body, blobErr := t.store.Blob(op.After.Hash)
			if blobErr != nil {
				return blobErr
			}
			if int64(len(body)) != op.After.Size {
				return fmt.Errorf("journal object size mismatch")
			}
		}
		if op.ExternalLink {
			allowed := false
			for _, mapping := range t.journal.Next.Manifest.Mappings {
				if mapping.Path == op.Path && model.Hash([]byte(mapping.Target)) == op.After.Hash {
					allowed = true
				}
			}
			if !allowed {
				return fmt.Errorf("unowned external link")
			}
		}
		current, _, readErr := ReadVersion(t.store.Root, op.Path, op.Before, respect)
		if readErr != nil {
			return readErr
		}
		if !current.Equal(op.Before) && (!replay || !current.Equal(op.After)) {
			return model.Fail(3, "file-changed", "File changed since planning; preserved: "+op.Path)
		}
		return nil
	})
}
func (t *Transaction) Apply() error {
	if !t.prepared {
		return fmt.Errorf("transaction is not prepared")
	}
	if err := t.preflight(true); err != nil {
		return err
	}
	respect := t.store.RespectExecutableBits(t.journal.Next.Manifest.RespectExecutableBits)
	// Prepare orders deletions first. They run one at a time so a file that is
	// about to become a directory (or the reverse) is gone before its
	// replacement is written; the independent writes then overlap.
	split := 0
	for split < len(t.journal.Operations) && !t.journal.Operations[split].After.Exists() {
		split++
	}
	for _, op := range t.journal.Operations[:split] {
		if err := t.applyOne(op, respect); err != nil {
			return err
		}
	}
	// Regular files are written before symbolic links, each group concurrently.
	// Windows decides at creation whether a link is a file link or a directory
	// link by looking at what its target is at that moment, so a link to a
	// directory made before the directory exists is the wrong kind and cannot be
	// opened. Elsewhere the order is invisible.
	files, links := splitLinkWrites(t.journal.Operations[split:])
	for _, batch := range [][]Operation{files, links} {
		if err := fsops.Parallel(context.Background(), len(batch), func(index int) error {
			return t.applyOne(batch[index], respect)
		}); err != nil {
			return err
		}
	}
	if err := t.store.Save(t.journal.Next); err != nil {
		return err
	}
	if err := t.store.Root.Remove(JournalPath); err != nil {
		return err
	}
	return t.store.collect(t.journal.Next)
}

// splitLinkWrites separates operations that write a symbolic link from the rest,
// keeping the journal order within each group.
func splitLinkWrites(operations []Operation) (files, links []Operation) {
	for _, op := range operations {
		if op.After.Kind == model.Symlink {
			links = append(links, op)
		} else {
			files = append(files, op)
		}
	}
	return files, links
}

// applyOne replays a single journalled operation. It is idempotent: a file
// already in its final state is left alone, and one that is neither the
// recorded before nor after state is preserved and reported.
func (t *Transaction) applyOne(op Operation, respect bool) error {
	current, _, wasDirectory, err := readVersion(t.store.Root, op.Path, op.Before, respect)
	if err != nil {
		return err
	}
	if current.Equal(op.After) {
		return nil
	}
	if !current.Equal(op.Before) {
		return model.Fail(3, "file-changed", "File changed during apply; preserved: "+op.Path)
	}
	if !op.After.Exists() {
		return t.store.Root.Remove(op.Path)
	}
	if wasDirectory {
		if err = t.removeEmptyDestination(op.Path); err != nil {
			return err
		}
	}
	body, err := t.afterBody(op.After)
	if err != nil {
		return err
	}
	switch op.After.Kind {
	case model.Regular:
		return t.store.Root.WriteAtomic(op.Path, eol.ToLane(body, op.After.Eol), os.FileMode(op.After.Mode))
	case model.Symlink:
		if op.ExternalLink {
			return t.store.Root.ReplaceDirectoryLink(op.Path, string(body))
		}
		return t.store.Root.ReplaceSymlink(op.Path, string(body), false)
	}
	return fmt.Errorf("unsupported file kind")
}

func (s *Store) Recover() error {
	body, err := s.Root.ReadFileLimit(JournalPath, model.MaxMetadataBytes)
	if err != nil {
		return err
	}
	var journal Journal
	if err = model.DecodeStrict(body, &journal); err != nil {
		return err
	}
	if journal.SchemaVersion != 1 || journal.Next.Manifest.Generation != journal.FromGeneration+1 {
		return fmt.Errorf("unsupported or corrupt journal")
	}
	return (&Transaction{store: s, journal: journal, prepared: true}).Apply()
}
