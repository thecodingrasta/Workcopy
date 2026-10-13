package state

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

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
	operations := make(map[string]Operation, len(t.journal.Operations))
	for _, op := range t.journal.Operations {
		operations[op.Path] = op
	}
	for _, op := range t.journal.Operations {
		if op.After.Exists() {
			if err = t.validateWriteShape(op, operations); err != nil {
				return err
			}
		}
	}
	for _, op := range t.journal.Operations {
		if err = fsops.ValidateSourcePath(op.Path); err != nil {
			return err
		}
		if op.After.Exists() {
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
		current, _, readErr := ReadVersion(t.store.Root, op.Path, op.Before, t.journal.Next.Manifest.RespectExecutableBits)
		if readErr != nil {
			return readErr
		}
		if !current.Equal(op.Before) && (!replay || !current.Equal(op.After)) {
			return model.Fail(3, "file-changed", "File changed since planning; preserved: "+op.Path)
		}
	}
	return nil
}
func (t *Transaction) Apply() error {
	if !t.prepared {
		return fmt.Errorf("transaction is not prepared")
	}
	if err := t.preflight(true); err != nil {
		return err
	}
	for _, op := range t.journal.Operations {
		current, _, err := ReadVersion(t.store.Root, op.Path, op.Before, t.journal.Next.Manifest.RespectExecutableBits)
		if err != nil {
			return err
		}
		if current.Equal(op.After) {
			continue
		}
		if !current.Equal(op.Before) {
			return model.Fail(3, "file-changed", "File changed during apply; preserved: "+op.Path)
		}
		if !op.After.Exists() {
			err = t.store.Root.Remove(op.Path)
		} else {
			if err = t.removeEmptyDestination(op.Path); err != nil {
				return err
			}
			var body []byte
			body, err = t.store.Blob(op.After.Hash)
			if err == nil {
				switch op.After.Kind {
				case model.Regular:
					err = t.store.Root.WriteAtomic(op.Path, body, os.FileMode(op.After.Mode))
				case model.Symlink:
					if op.ExternalLink {
						err = t.store.Root.ReplaceDirectoryLink(op.Path, string(body))
					} else {
						err = t.store.Root.ReplaceSymlink(op.Path, string(body), false)
					}
				default:
					err = fmt.Errorf("unsupported file kind")
				}
			}
		}
		if err != nil {
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
