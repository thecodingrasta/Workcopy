package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncCombinesIndependentEdits(t *testing.T) {
	r, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "a.txt"), []byte("LOCAL\nsecond\nthird\nfourth\nfifth\n"), 0644)
	r.Write("a.txt", "first\nsecond\nthird\nfourth\nUPSTREAM\n")
	r.Commit("upstream")
	report, err := e.Sync(context.Background(), "task", false)
	if err != nil || len(report.Conflicts) != 0 {
		t.Fatal(report, err)
	}
	b, _ := os.ReadFile(filepath.Join(lane, "a.txt"))
	if string(b) != "LOCAL\nsecond\nthird\nfourth\nUPSTREAM\n" {
		t.Fatal("lost edit", string(b))
	}
}
func TestResolutionRetainsContributionAndOtherConflicts(t *testing.T) {
	r, e, lane := fixtureLane(t)
	for _, name := range []string{"a.txt", "b.txt"} {
		os.WriteFile(filepath.Join(lane, name), []byte("local\n"), 0644)
		r.Write(name, "upstream\n")
	}
	r.Commit("conflicting upstream")
	result, err := e.Sync(context.Background(), "task", false)
	if err != nil || len(result.Conflicts) != 2 {
		t.Fatal(result, err)
	}
	conflict := result.Conflicts[0]
	_, err = e.Resolve(context.Background(), "task", conflict.Path, ResolveOptions{UseLocal: true, ExpectedConflict: conflict.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	report, err := e.Status(context.Background(), "task")
	if err != nil || len(report.Conflicts) != 1 || len(report.Changes) != 2 {
		t.Fatal(report, err)
	}
	if _, err = e.Destroy(context.Background(), "task", false, false); err == nil {
		t.Fatal("resolved contribution became disposable")
	}
	remaining := report.Conflicts[0]
	_, err = e.Resolve(context.Background(), "task", remaining.Path, ResolveOptions{UseLocal: true, ExpectedConflict: remaining.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	status, err := e.Status(context.Background(), "task")
	if err != nil || len(status.Conflicts) != 0 || len(status.Changes) != 2 {
		t.Fatal(status, err)
	}
}
func TestUpstreamDeleteResolutionDoesNotTruncateOnFailure(t *testing.T) {
	r, e, lane := fixtureLane(t)
	os.WriteFile(filepath.Join(lane, "b.txt"), []byte("valuable local\n"), 0644)
	os.Remove(filepath.Join(r.Path, "b.txt"))
	r.Commit("delete")
	synced, err := e.Sync(context.Background(), "task", false)
	if err != nil || len(synced.Conflicts) != 1 {
		t.Fatal(synced, err)
	}
	conflict := synced.Conflicts[0]
	if _, err = e.Resolve(context.Background(), "task", "b.txt", ResolveOptions{UseUpstream: true, ExpectedConflict: "stale"}, false); err == nil {
		t.Fatal("accepted stale token")
	}
	b, _ := os.ReadFile(filepath.Join(lane, "b.txt"))
	if string(b) != "valuable local\n" {
		t.Fatal("truncated local file")
	}
	if _, err = e.Resolve(context.Background(), "task", "b.txt", ResolveOptions{UseUpstream: true, ExpectedConflict: conflict.ID}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(lane, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("upstream deletion not applied")
	}
}
func TestSyncDryRunLeavesSourceAndMetadataUnchanged(t *testing.T) {
	r, e, lane := fixtureLane(t)
	r.Write("b.txt", "upstream\n")
	r.Commit("upstream")
	before, _ := os.ReadFile(filepath.Join(lane, ".workcopy", "manifest.json"))
	if _, err := e.Sync(context.Background(), "task", true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(lane, ".workcopy", "manifest.json"))
	b, _ := os.ReadFile(filepath.Join(lane, "b.txt"))
	if string(before) != string(after) || string(b) != "base\n" {
		t.Fatal("dry-run mutated lane")
	}
}
