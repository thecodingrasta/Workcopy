package workspace

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"workcopy/internal/model"
	"workcopy/internal/state"
)

func TestCreationRecoversFromRegistrationBeforeAnyLaneFiles(t *testing.T) {
	_, e, lane := fixtureLane(t)
	opened, err := e.openLane("task")
	if err != nil {
		t.Fatal(err)
	}
	record, seed := opened.record, opened.snapshot
	opened.close()
	record.Phase, record.Seed = "creating", &seed
	registry, lock, err := e.rootLock()
	if err != nil {
		t.Fatal(err)
	}
	if err = saveRegistration(registry, record); err != nil {
		t.Fatal(err)
	}
	lock.Close()
	registry.Close()
	if err = os.RemoveAll(lane); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Recover(context.Background(), "task", false); err != nil {
		t.Fatal(err)
	}
	report, err := e.Status(context.Background(), "task")
	if err != nil || len(report.Changes) != 0 {
		t.Fatal(report, err)
	}
	if body, err := os.ReadFile(filepath.Join(lane, "b.txt")); err != nil || string(body) != "base\n" {
		t.Fatal(string(body), err)
	}
}
func TestQuarantinedDeletionResumesWithoutFollowingSharedTargets(t *testing.T) {
	e, _, cache := sharingFixture(t)
	if _, err := e.Share(context.Background(), "daily", "packages", cache, "fixture", true, false); err != nil {
		t.Fatal(err)
	}
	opened, err := e.openLane("daily")
	if err != nil {
		t.Fatal(err)
	}
	record := opened.record
	record.Phase = "deleting"
	record.Quarantine = filepath.Join(e.WorkRoot, ".workcopy-trash-"+record.ID)
	record.DeleteFiles = map[string]model.FileVersion{}
	err = opened.root.Walk(func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		if entry.IsDir() {
			record.DeleteDirs = append(record.DeleteDirs, name)
			return nil
		}
		version, _, err := state.ReadVersion(opened.root, name, model.FileVersion{}, true)
		if err != nil {
			return err
		}
		record.DeleteFiles[name] = version
		return nil
	})
	opened.close()
	if err != nil {
		t.Fatal(err)
	}
	registry, lock, err := e.rootLock()
	if err != nil {
		t.Fatal(err)
	}
	if err = saveRegistration(registry, record); err != nil {
		t.Fatal(err)
	}
	if err = registry.Rename(record.Name, filepath.Base(record.Quarantine)); err != nil {
		t.Fatal(err)
	}
	lock.Close()
	registry.Close()
	if err = os.Remove(filepath.Join(record.Quarantine, "A", "Source.cs")); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Recover(context.Background(), "daily", false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(record.Quarantine); !os.IsNotExist(err) {
		t.Fatal("quarantine remains")
	}
	if body, err := os.ReadFile(filepath.Join(cache, "preserve.txt")); err != nil || string(body) != "shared content" {
		t.Fatal("shared target changed", err)
	}
}
func TestInterruptedReviewPreparationRestoresMissingFiles(t *testing.T) {
	_, e, lane := fixtureLane(t)
	local := []byte("local\n")
	if err := os.WriteFile(filepath.Join(lane, "b.txt"), local, 0644); err != nil {
		t.Fatal(err)
	}
	report, err := e.Merge(context.Background(), "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := e.openLane("task")
	if err != nil {
		t.Fatal(err)
	}
	receipt := *report.Review
	receipt.State = "preparing"
	if _, err = opened.store.PutBlob(local); err != nil {
		t.Fatal(err)
	}
	if _, err = replaceReview(opened.store, receipt); err != nil {
		t.Fatal(err)
	}
	opened.close()
	if err = os.Remove(filepath.Join(receipt.Path, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Recover(context.Background(), "task", false); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(receipt.Path, "b.txt")); err != nil || string(body) != string(local) {
		t.Fatal(string(body), err)
	}
}
