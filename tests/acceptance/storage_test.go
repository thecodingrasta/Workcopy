package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

// storage through the shipped binary: it measures a real lane from inside it and
// from outside it, tells private build output from bytes shared with the outside,
// refuses bad input with the usual exit codes, and changes nothing.
func TestStorageThroughTheBinary(t *testing.T) {
	r := testutil.NewFixtureRepository(t, "large-monorepo")
	root := filepath.Join(r.Parent, "lanes")
	if out, _, code := run(t, r.Path, nil, "create", "cost", "--project", "project-01", "--root", root, "--json"); code != 0 {
		t.Fatalf("create: %d %s", code, out)
	}
	lane := filepath.Join(root, "cost")

	// Build output of two kinds: private, and a hard link to a file that lives
	// outside the lane (a package store, for example).
	private := filepath.Join(lane, "project-01", "bin", "Debug", "private.dll")
	if err := os.MkdirAll(filepath.Dir(private), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private, make([]byte, 40000), 0o600); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(r.Parent, "package-store")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(store, "pkg.dll")
	if err := os.WriteFile(external, make([]byte, 60000), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := os.Link(external, filepath.Join(lane, "project-01", "bin", "Debug", "pkg.dll")) == nil
	before, _ := os.ReadFile(private)

	// From outside the lane, by name.
	out, stderr, code := run(t, r.Path, nil, "storage", "cost", "--root", root, "--json")
	var report workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &report) != nil || report.Storage == nil || report.Command != "storage" {
		t.Fatalf("storage: %d\n%s\n%s", code, out, stderr)
	}
	detail := report.Storage
	if detail.Lane != "cost" || detail.Volume.FileSystem == "" || len(detail.Account.Largest) == 0 {
		t.Fatalf("incomplete report: %+v", detail)
	}
	source, output := detail.Account.Classes["source"], detail.Account.Classes["likely-output"]
	if source == nil || source.Files == 0 || output == nil || output.Files < 1 || output.LogicalBytes < 40000 {
		t.Fatalf("classes: %+v", detail.Account.Classes)
	}
	if linked && (output.SharedBytes < 60000 || output.LinkedFiles != 1) {
		t.Fatalf("a file shared with the outside must be reported as shared: %+v", output)
	}
	if output.ReclaimableBytes < 40000 {
		t.Fatalf("private output must be reclaimable: %+v", output)
	}

	// From inside the lane, with no name or root: the lane is inferred.
	inside, _, code := run(t, lane, nil, "storage", "--json")
	var viaCwd workspace.Report
	if code != 0 || json.Unmarshal([]byte(inside), &viaCwd) != nil || viaCwd.Storage == nil || viaCwd.Storage.Lane != "cost" {
		t.Fatalf("storage inside a lane: %d %s", code, inside)
	}

	// Human output names the places the space went.
	text, _, code := run(t, r.Path, nil, "storage", "cost", "--root", root)
	if code != 0 || !strings.Contains(text, "Largest directories") || !strings.Contains(text, "Read-only") || !strings.Contains(text, "likely-output") {
		t.Fatalf("human output: %d\n%s", code, text)
	}

	// Refusals use the normal exit codes and write nothing.
	if _, _, code = run(t, r.Path, nil, "storage", "cost", "--root", root, "--force"); code != 2 {
		t.Fatalf("an unknown flag must be a usage error, got %d", code)
	}
	if out, _, code = run(t, r.Path, nil, "storage", "nope", "--root", root, "--json"); code == 0 || errorCode(t, out) == "" {
		t.Fatalf("a missing lane must fail with an error: %d %s", code, out)
	}
	if after, _ := os.ReadFile(private); string(after) != string(before) {
		t.Fatal("measuring changed a lane file")
	}
	if _, err := os.Stat(external); err != nil {
		t.Fatal("measuring touched the external file")
	}
}
