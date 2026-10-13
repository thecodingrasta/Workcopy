package state

import (
	"encoding/json"
	"os"
	"testing"
	"workcopy/internal/fsops"
	"workcopy/internal/model"
)

func fixture(t *testing.T) (*Store, *fsops.Root, model.Snapshot) {
	t.Helper()
	root, err := fsops.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	s := New(root)
	snapshot := model.Snapshot{Manifest: model.Manifest{SchemaVersion: 1, ID: "0123456789abcdef0123456789abcdef", Generation: 1, Phase: "ready"}, Baseline: model.Baseline{Files: map[string]model.FileVersion{}}, Conflicts: []model.Conflict{}}
	if err = s.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	return s, root, snapshot
}
func TestRecoveryReplaysPartialTransaction(t *testing.T) {
	s, root, next := fixture(t)
	next.Manifest.Generation++
	tx, err := s.Begin(next)
	if err != nil {
		t.Fatal(err)
	}
	after := model.Version(model.Regular, 0644, []byte("new"))
	for _, name := range []string{"a", "b"} {
		if err = tx.Add(name, model.FileVersion{}, after, []byte("new"), false); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err = root.WriteAtomic("a", []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		b, err := root.ReadFile(name)
		if err != nil || string(b) != "new" {
			t.Fatal(name, string(b), err)
		}
	}
	loaded, err := s.Load()
	if err != nil || loaded.Manifest.Generation != 2 {
		t.Fatal(loaded, err)
	}
}
func TestRecoveryPreservesLaterEdits(t *testing.T) {
	s, root, next := fixture(t)
	next.Manifest.Generation++
	tx, _ := s.Begin(next)
	after := model.Version(model.Regular, 0644, []byte("proposed"))
	tx.Add("a", model.FileVersion{}, after, []byte("proposed"), false)
	tx.Prepare()
	root.WriteAtomic("a", []byte("new user edit"), 0644)
	if err := s.Recover(); err == nil {
		t.Fatal("overwrote newer edit")
	}
	b, _ := root.ReadFile("a")
	if string(b) != "new user edit" {
		t.Fatal(string(b))
	}
	if _, err := root.Lstat(JournalPath); os.IsNotExist(err) {
		t.Fatal("lost recovery journal")
	}
}
func TestOnlyOneWriterCanHoldLaneLock(t *testing.T) {
	s, _, _ := fixture(t)
	first, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := s.Lock()
	if err == nil {
		second.Close()
		t.Fatal("second writer acquired lock")
	}
}

func TestRejectsCorruptFileKinds(t *testing.T) {
	s, root, snapshot := fixture(t)
	snapshot.Baseline.Files["valuable"] = model.FileVersion{Kind: "corrupt"}
	body, _ := json.Marshal(snapshot)
	if err := root.WriteAtomic(ManifestPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("unknown file kind treated as missing")
	}
}

func TestRejectsJournalIdentityReplacement(t *testing.T) {
	s, root, snapshot := fixture(t)
	if err := s.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	next := snapshot
	next.Manifest.Generation++
	next.Manifest.ID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	journal := Journal{SchemaVersion: 1, WorkspaceID: snapshot.Manifest.ID, FromGeneration: 1, Next: next}
	body, _ := json.Marshal(journal)
	root.WriteAtomic(JournalPath, body, 0600)
	if err := s.Recover(); err == nil {
		t.Fatal("journal replaced the owned workspace identity")
	}
	loaded, _ := s.Load()
	if loaded.Manifest.ID != snapshot.Manifest.ID {
		t.Fatal("identity changed")
	}
}

func TestWindowsJunctionIsALinkCandidateButOtherSpecialFilesAreNot(t *testing.T) {
	cases := []struct {
		mode     os.FileMode
		platform string
		want     bool
	}{
		{os.ModeSymlink, "linux", true}, {os.ModeSymlink, "windows", true},
		{os.ModeIrregular, "windows", true}, {os.ModeIrregular, "linux", false},
		{os.ModeDir, "windows", false}, {os.ModeNamedPipe, "windows", false}, {0600, "windows", false},
	}
	for _, c := range cases {
		if got := linkCandidate(c.mode, c.platform); got != c.want {
			t.Errorf("mode=%s platform=%s: got %v, want %v", c.mode, c.platform, got, c.want)
		}
	}
}

func TestRecoveryFinishesDirectoryReplacementAfterPartialDeletion(t *testing.T) {
	store, root, next := fixture(t)
	old := model.Version(model.Regular, 0644, []byte("old"))
	after := model.Version(model.Regular, 0644, []byte("replacement"))
	if err := root.WriteAtomic("shape/nested/old.txt", []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	next.Manifest.Generation++
	next.Baseline.Files["shape"] = after
	tx, err := store.Begin(next)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Add("shape", model.FileVersion{}, after, []byte("replacement"), false); err != nil {
		t.Fatal(err)
	}
	if err = tx.Add("shape/nested/old.txt", old, model.FileVersion{}, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = tx.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err = root.Remove("shape/nested/old.txt"); err != nil {
		t.Fatal(err)
	}
	if err = store.Recover(); err != nil {
		t.Fatal(err)
	}
	body, err := root.ReadFile("shape")
	if err != nil || string(body) != "replacement" {
		t.Fatal(string(body), err)
	}
}
func TestTransactionRefusesUnplannedParentReplacement(t *testing.T) {
	store, root, next := fixture(t)
	if err := root.WriteAtomic("parent", []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	next.Manifest.Generation++
	tx, err := store.Begin(next)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("new")
	if err = tx.Add("parent/child", model.FileVersion{}, model.Version(model.Regular, 0644, body), body, false); err != nil {
		t.Fatal(err)
	}
	if err = tx.Prepare(); err == nil {
		t.Fatal("prepared a child write through an unplanned parent file")
	}
	if body, err = root.ReadFile("parent"); err != nil || string(body) != "keep" {
		t.Fatal("parent changed")
	}
}

func TestStagedOperationVerifiesContentBeforePreparingJournal(t *testing.T) {
	store, root, next := fixture(t)
	next.Manifest.Generation++
	body := []byte("staged contribution")
	after := model.Version(model.Regular, 0644, body)
	tx, err := store.Begin(next)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.AddStaged("source", model.FileVersion{}, after, false); err != nil {
		t.Fatal(err)
	}
	if err = tx.Prepare(); err == nil {
		t.Fatal("prepared without staged content")
	}
	if _, err = root.Lstat(JournalPath); !os.IsNotExist(err) {
		t.Fatal("invalid staged input created a journal")
	}
	if _, err = store.PutBlob(body); err != nil {
		t.Fatal(err)
	}
	if err = tx.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err = tx.Apply(); err != nil {
		t.Fatal(err)
	}
	actual, err := root.ReadFile("source")
	if err != nil || string(actual) != string(body) {
		t.Fatal(string(actual), err)
	}
}
