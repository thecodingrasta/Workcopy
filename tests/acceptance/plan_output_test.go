package acceptance

import (
	"fmt"
	"strings"
	"testing"

	"workcopy/internal/testutil"
)

// WC-021: `create --json` printed every file of a 4,160-file plan, 274,208 bytes,
// which an agent with an output cap could not even read. Plans that list files now
// say how many and leave the names out unless --include-files is given, as
// `merge --json` already did.

func wideFixture(t *testing.T, files int) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	for n := 0; n < files; n++ {
		r.Write(fmt.Sprintf("src/project%02d/folder%d/a-fairly-descriptive-file-name-%04d.cs", n%20, n%5, n), fmt.Sprintf("// file %d\n", n))
	}
	r.Write(".workcopy.json", `{"schemaVersion":1,"projects":[{"id":"core","path":"src/project00"},{"id":"extra","path":"src/project01"}]}`)
	r.Commit("wide")
	return r
}

func TestPlanOutputIsSmallUnlessFilesAreRequested(t *testing.T) {
	t.Parallel()
	const files = 1200
	r := wideFixture(t, files)
	root := r.Parent + "/lanes"

	for _, dryRun := range []bool{true, false} {
		args := []string{"create", fmt.Sprintf("lane%v", dryRun), "--project", "core", "--root", root, "--json"}
		if dryRun {
			args = append(args, "--dry-run")
		}
		stdout, _, code := run(t, r.Path, nil, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d %s", args, code, stdout)
		}
		if len(stdout) > 6000 {
			t.Errorf("%v: %d bytes of JSON; a plan must be a summary by default", args, len(stdout))
		}
	}

	// The summary says how big the plan is and that the names were left out.
	report := laneReport(t, r.Path, nil, 0, "create", "summary", "--all", "--root", root, "--dry-run")
	if report.Plan == nil || report.Plan.FileCount != files+1 || !report.Plan.FilesOmitted || len(report.Plan.Files) != 0 {
		t.Fatalf("expected %d files counted and omitted, got %+v", files+1, report.Plan)
	}

	// Asking for them lists every one, so nothing is lost.
	full := laneReport(t, r.Path, nil, 0, "create", "full", "--all", "--root", root, "--dry-run", "--include-files")
	if full.Plan.FilesOmitted || len(full.Plan.Files) != files+1 || full.Plan.FileCount != files+1 {
		t.Fatalf("--include-files must list all %d files, got %d (omitted=%v)", files+1, len(full.Plan.Files), full.Plan.FilesOmitted)
	}

	// The human output reports the true count in both modes.
	text, _, _ := run(t, r.Path, nil, "create", "human", "--all", "--root", root, "--dry-run")
	if !strings.Contains(text, fmt.Sprintf("%d files", files+1)) {
		t.Errorf("human output must state the file count, got:\n%s", text)
	}
}

func TestSyncAndExpandPlansFollowTheSameRule(t *testing.T) {
	t.Parallel()
	r := wideFixture(t, 900)
	root := r.Parent + "/lanes"
	lane := laneReport(t, r.Path, nil, 0, "create", "task", "--project", "core", "--root", root).Workspace.Path

	expand := laneReport(t, lane, nil, 0, "expand", "extra", "--dry-run")
	if expand.Plan.FileCount == 0 || !expand.Plan.FilesOmitted || len(expand.Plan.Files) != 0 {
		t.Fatalf("expand --dry-run: %+v", expand.Plan)
	}
	if listed := laneReport(t, lane, nil, 0, "expand", "extra", "--dry-run", "--include-files"); len(listed.Plan.Files) != expand.Plan.FileCount {
		t.Fatalf("expand --include-files lists %d, the summary counted %d", len(listed.Plan.Files), expand.Plan.FileCount)
	}

	for n := 0; n < 900; n += 2 {
		r.Write(fmt.Sprintf("src/project%02d/folder%d/a-fairly-descriptive-file-name-%04d.cs", n%20, n%5, n), fmt.Sprintf("// upstream edit %d\n", n))
	}
	r.Commit("upstream")
	sync := laneReport(t, lane, nil, 0, "sync", "--dry-run")
	if sync.Plan.FileCount == 0 || !sync.Plan.FilesOmitted || len(sync.Plan.Files) != 0 {
		t.Fatalf("sync --dry-run: %+v", sync.Plan)
	}
	if listed := laneReport(t, lane, nil, 0, "sync", "--dry-run", "--include-files"); len(listed.Plan.Files) != sync.Plan.FileCount {
		t.Fatalf("sync --include-files lists %d, the summary counted %d", len(listed.Plan.Files), sync.Plan.FileCount)
	}

	// Commands whose plan names a single lane or path are unaffected.
	if destroy := laneReport(t, r.Path, nil, 0, "destroy", "task", "--root", root, "--dry-run"); destroy.Plan.FilesOmitted || len(destroy.Plan.Files) != 1 || destroy.Plan.Files[0] != "task" {
		t.Fatalf("destroy names the lane, it is not an inventory: %+v", destroy.Plan)
	}
}
