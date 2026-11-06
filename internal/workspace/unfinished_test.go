package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/fsops"
)

// A creation that cannot finish used to strand the lane: every command refused
// it as not ready, recover only retries, and destroy would not touch it. The
// owner's only way out was editing Workcopy's registry by hand. destroy --force
// now discards it, and only it.
func TestDestroyForceDiscardsALaneWhoseCreationNeverFinished(t *testing.T) {
	t.Parallel()
	r := wideRepository(t, 520)
	background := context.Background()
	e, err := New(background, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	neighbour, err := e.Create(background, "neighbour", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := cancelAfter(256)
	defer cancel()
	if _, err = e.Create(ctx, "stuck", CreateOptions{All: true}, false); err == nil {
		t.Fatal("the interrupted creation must report failure")
	} else if !strings.Contains(err.Error(), "destroy stuck --force") {
		t.Fatalf("the failure must say how to discard the lane, got: %v", err)
	}
	stuck := filepath.Join(e.WorkRoot, "stuck")
	if _, statErr := os.Lstat(stuck); statErr != nil {
		t.Fatalf("the interrupted creation should have left a partial directory: %v", statErr)
	}
	pins := func() int { return len(strings.Fields(r.Git("for-each-ref", "--format=%(refname)", "refs/workcopy/"))) }
	if pins() != 2 {
		t.Fatalf("expected one pin per registered lane, got %d", pins())
	}

	// Without --force it is refused, and the message names the way out.
	if _, err = e.Destroy(background, "stuck", false, false); failureCode(t, err) != "recovery-required" || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("a plain destroy must explain --force: %v", err)
	}
	// The same message reaches every other command through openLane.
	if _, err = e.Status(background, "stuck"); failureCode(t, err) != "recovery-required" || !strings.Contains(err.Error(), "destroy NAME --force") {
		t.Fatalf("status must point at destroy --force: %v", err)
	}
	// A dry run changes nothing.
	if report, dryErr := e.Destroy(background, "stuck", true, true); dryErr != nil || report.Plan == nil {
		t.Fatalf("dry run: %+v %v", report, dryErr)
	}
	if _, statErr := os.Lstat(stuck); statErr != nil || pins() != 2 {
		t.Fatalf("a dry run must not delete anything: %v pins=%d", statErr, pins())
	}

	report, err := e.Destroy(background, "stuck", true, false)
	if err != nil || !strings.Contains(report.Message, "Unfinished lane discarded") {
		t.Fatalf("force must discard the unfinished lane: %+v %v", report, err)
	}
	if _, statErr := os.Lstat(stuck); !os.IsNotExist(statErr) {
		t.Fatalf("the partial directory must be gone: %v", statErr)
	}
	if leftover, _ := filepath.Glob(filepath.Join(e.WorkRoot, ".workcopy-trash-*")); len(leftover) != 0 {
		t.Fatalf("quarantine must be cleaned up: %v", leftover)
	}
	if pins() != 1 {
		t.Fatalf("only the neighbour's pin should remain, got %d", pins())
	}
	listed, err := e.List(background)
	if err != nil || len(listed.Workspaces) != 1 || listed.Workspaces[0].Name != "neighbour" {
		t.Fatalf("only the neighbour should remain registered: %+v %v", listed.Workspaces, err)
	}
	// The healthy neighbour is untouched and the name is free again.
	if status, statusErr := e.Status(background, "neighbour"); statusErr != nil || len(status.Changes) != 0 {
		t.Fatalf("the neighbour must be unaffected: %+v %v", status.Changes, statusErr)
	}
	if _, statErr := os.Lstat(neighbour.Workspace.Path); statErr != nil {
		t.Fatal(statErr)
	}
	if _, err = e.Create(background, "stuck", CreateOptions{All: true}, false); err != nil {
		t.Fatalf("the discarded name must be reusable: %v", err)
	}
}

// The failure can happen before the lane directory exists at all.
func TestDestroyForceDiscardsAnUnfinishedLaneWithNoDirectory(t *testing.T) {
	t.Parallel()
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
	if _, err = e.Destroy(context.Background(), "task", true, false); err != nil {
		t.Fatalf("an unfinished lane with no directory must still be discardable: %v", err)
	}
	if listed, listErr := e.List(context.Background()); listErr != nil || len(listed.Workspaces) != 0 {
		t.Fatalf("nothing should remain registered: %+v %v", listed.Workspaces, listErr)
	}
}

// Force on a lane that is ready keeps its meaning: it discards the lane's own
// uncommitted work, and nothing about it changed.
func TestDestroyForceOnAReadyLaneStillRequiresAHealthyLane(t *testing.T) {
	t.Parallel()
	_, e, _ := fixtureLane(t)
	if _, err := e.Destroy(context.Background(), "no-such-lane", true, false); failureCode(t, err) != "workspace-not-registered" {
		t.Fatalf("an unknown lane stays unknown with --force: %v", err)
	}
}

// A volume that equates two tracked paths (macOS does for canonically
// equivalent Unicode) must be refused before anything is written: no directory,
// no registration, no pinned ref, no probe residue, and the same name usable
// afterwards. The test stands in for that volume, because the hosts that run it
// do not all have one; the probe itself is tested against real volumes in fsops.
func TestNameCollisionOnTheLaneVolumeIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	r := wideRepository(t, 40)
	background := context.Background()
	e, err := New(background, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	was := findNameCollision
	t.Cleanup(func() { findNameCollision = was })
	var asked []string
	findNameCollision = func(_ *fsops.Root, names []string) (*fsops.NameCollision, error) {
		asked = names
		return &fsops.NameCollision{First: "docs/caf\u00e9.txt", Second: "docs/cafe\u0301.txt"}, nil
	}
	_, err = e.Create(background, "task", CreateOptions{All: true}, false)
	if failureCode(t, err) != "name-collision" {
		t.Fatalf("want name-collision, got %v", err)
	}
	for _, must := range []string{`"docs/caf\u00e9.txt"`, `"docs/cafe\u0301.txt"`, "Nothing was created", "--project"} {
		if !strings.Contains(err.Error(), must) {
			t.Errorf("the message must contain %s so two look-alike names can be told apart: %v", must, err)
		}
	}
	if len(asked) != 40 {
		t.Errorf("the probe must be given every selected path, got %d", len(asked))
	}
	if _, statErr := os.Lstat(filepath.Join(e.WorkRoot, "task")); !os.IsNotExist(statErr) {
		t.Errorf("no lane directory may exist: %v", statErr)
	}
	if registrations, _ := filepath.Glob(filepath.Join(e.WorkRoot, ".workcopy-registry", "*.json")); len(registrations) != 0 {
		t.Errorf("no registration may exist: %v", registrations)
	}
	if pins := strings.TrimSpace(r.Git("for-each-ref", "refs/workcopy/")); pins != "" {
		t.Errorf("no pinned ref may exist:\n%s", pins)
	}
	findNameCollision = was
	if _, err = e.Create(background, "task", CreateOptions{All: true}, false); err != nil {
		t.Fatalf("the name must be usable once the collision is gone: %v", err)
	}

	// A probe that fails for its own reasons is an error, never silent success.
	findNameCollision = func(*fsops.Root, []string) (*fsops.NameCollision, error) {
		return nil, os.ErrPermission
	}
	if _, err = e.Create(background, "other", CreateOptions{All: true}, false); err == nil {
		t.Fatal("a failing probe must stop creation")
	}
	if _, statErr := os.Lstat(filepath.Join(e.WorkRoot, "other")); !os.IsNotExist(statErr) {
		t.Errorf("a failed probe must leave no lane directory: %v", statErr)
	}
}
