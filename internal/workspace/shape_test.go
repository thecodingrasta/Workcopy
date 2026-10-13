package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"workcopy/internal/state"
	"workcopy/internal/testutil"
)

func shapeFixture(t *testing.T, folder bool) (*testutil.Repository, *Engine, string) {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write(".gitignore", "*.private\n")
	name := "shape"
	if folder {
		name = "shape/nested/file.txt"
	}
	r.Write(name, "base\n")
	r.Commit("base shape")
	e, err := New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(context.Background(), "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	return r, e, created.Workspace.Path
}
func replaceShape(t *testing.T, directory string, folder bool) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(directory, "shape")); err != nil {
		t.Fatal(err)
	}
	name := "shape"
	if folder {
		name = "shape/nested/file.txt"
		if err := os.MkdirAll(filepath.Dir(filepath.Join(directory, name)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte("replacement\n"), 0644); err != nil {
		t.Fatal(err)
	}
}
func TestSyncHandlesFileDirectoryTransitions(t *testing.T) {
	for _, folder := range []bool{false, true} {
		t.Run(map[bool]string{false: "file-to-directory", true: "directory-to-file"}[folder], func(t *testing.T) {
			r, e, lane := shapeFixture(t, folder)
			replaceShape(t, r.Path, !folder)
			r.Commit("change shape")
			if _, err := e.Sync(context.Background(), "task", false); err != nil {
				t.Fatal(err)
			}
			report, err := e.Status(context.Background(), "task")
			if err != nil || len(report.Changes) != 0 {
				t.Fatal("shape update not clean", report, err)
			}
			name := "shape"
			if !folder {
				name = "shape/nested/file.txt"
			}
			body, err := os.ReadFile(filepath.Join(lane, name))
			if err != nil || string(body) != "replacement\n" {
				t.Fatal(string(body), err)
			}
		})
	}
}
func TestLocalFileDirectoryTransitionsCanBeReviewed(t *testing.T) {
	for _, folder := range []bool{false, true} {
		t.Run(map[bool]string{false: "file-to-directory", true: "directory-to-file"}[folder], func(t *testing.T) {
			_, e, lane := shapeFixture(t, folder)
			replaceShape(t, lane, !folder)
			report, err := e.Merge(context.Background(), "task", MergeOptions{}, false)
			if err != nil || report.Review == nil {
				t.Fatal("local shape change cannot be reviewed", report, err)
			}
			if _, err = e.Merge(context.Background(), "task", MergeOptions{Commit: true, Approve: true, ReviewID: report.Review.ID, Message: "review shape change"}, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestDirectoryReplacementPreservesIgnoredLocalFiles(t *testing.T) {
	r, e, lane := shapeFixture(t, true)
	secret := filepath.Join(lane, "shape", "keep.private")
	if err := os.WriteFile(secret, []byte("synthetic private data"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(lane, state.ManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	replaceShape(t, r.Path, false)
	r.Commit("replace directory")
	if _, err = e.Sync(context.Background(), "task", false); err == nil {
		t.Fatal("replaced nonempty local directory")
	}
	after, err := os.ReadFile(filepath.Join(lane, state.ManifestPath))
	if err != nil || string(before) != string(after) {
		t.Fatal("blocked transition changed state", err)
	}
	if _, err = os.Stat(filepath.Join(lane, state.JournalPath)); !os.IsNotExist(err) {
		t.Fatal("blocked transition left a prepared journal")
	}
	if body, err := os.ReadFile(secret); err != nil || string(body) != "synthetic private data" {
		t.Fatal("local-only file was damaged")
	}
}
