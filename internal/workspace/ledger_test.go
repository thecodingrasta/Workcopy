package workspace

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"workcopy/internal/storage"
	testfixture "workcopy/internal/testutil"
)

func ledgerLane(t *testing.T, tracked map[string]string, config string) (*Engine, string) {
	t.Helper()
	repository := testfixture.NewRepository(t)
	for name, body := range tracked {
		repository.Write(name, body)
	}
	repository.Commit("base")
	engine, err := New(context.Background(), repository.Path, filepath.Join(repository.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	options := CreateOptions{All: true}
	if config != "" {
		options.ConfigData = []byte(config)
	}
	created, err := engine.Create(context.Background(), "daily", options, false)
	if err != nil {
		t.Fatal(err)
	}
	return engine, created.Workspace.Path
}

func putFile(t *testing.T, lane, name string, size int) {
	t.Helper()
	full := filepath.Join(lane, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(strings.Repeat("y", size)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func storageOf(t *testing.T, engine *Engine) *StorageReport {
	t.Helper()
	report, err := engine.Storage(context.Background(), "daily")
	if err != nil {
		t.Fatal(err)
	}
	if report.Storage == nil || report.Command != "storage" {
		t.Fatalf("no storage detail: %+v", report)
	}
	return report.Storage
}

func class(t *testing.T, detail *StorageReport, name storage.Class) storage.Totals {
	t.Helper()
	totals := detail.Account.Classes[name]
	if totals == nil {
		return storage.Totals{}
	}
	return *totals
}

// Tracked files stay "source" even inside a directory called bin; only untracked
// files in conventional output directories are "likely-output".
func TestStorageSplitsSourceFromOutputWithoutMisfilingTrackedFiles(t *testing.T) {
	engine, lane := ledgerLane(t, map[string]string{
		"A/Source.cs":         "class S {}\n",
		"A/bin/tracked.txt":   "tracked inside a bin directory\n",
		"README.md":           "readme\n",
		"tools/node_modules/": "",
	}, "")
	putFile(t, lane, "A/bin/Debug/A.dll", 40000)
	putFile(t, lane, "A/obj/Debug/A.pdb", 20000)
	putFile(t, lane, "web/node_modules/p/index.js", 10000)
	putFile(t, lane, "notes/scratch.txt", 500)
	detail := storageOf(t, engine)

	source, output, other := class(t, detail, classSource), class(t, detail, classLikelyOutput), class(t, detail, classOther)
	if source.Files < 3 {
		t.Fatalf("tracked files (including the one under bin/) must be source: %+v", source)
	}
	if output.Files != 3 || output.LogicalBytes != 70000 {
		t.Fatalf("likely-output = %+v, want 3 files and 70000 logical bytes", output)
	}
	if other.Files != 1 || other.LogicalBytes != 500 {
		t.Fatalf("other-local = %+v, want the scratch note only", other)
	}
	if class(t, detail, classMetadata).Files == 0 {
		t.Fatal("Workcopy's own metadata was not accounted for")
	}
	if output.AllocatedBytes < 70000 || output.ReclaimableBytes != output.AllocatedBytes {
		t.Fatalf("private build output must be fully reclaimable: %+v", output)
	}
}

// The owner's destroy.discard patterns win over the naming heuristic, and a
// repository-committed policy is ignored (control).
func TestStorageDeclaredOutputNeedsAnOwnerPolicy(t *testing.T) {
	const policy = `{"schemaVersion":1,"discovery":{"adapters":["generic"]},"destroy":{"discard":["**/bin/**"]}}`
	tracked := map[string]string{"A/Source.cs": "class S {}\n"}

	engine, lane := ledgerLane(t, tracked, policy)
	putFile(t, lane, "A/bin/a.dll", 30000)
	detail := storageOf(t, engine)
	if got := class(t, detail, classDeclaredOutput); got.Files != 1 || got.LogicalBytes != 30000 {
		t.Fatalf("owner-declared output = %+v", got)
	}
	if got := class(t, detail, classLikelyOutput); got.Files != 0 {
		t.Fatalf("declared output was also counted as likely output: %+v", got)
	}

	control, controlLane := ledgerLane(t, tracked, "")
	putFile(t, controlLane, "A/bin/a.dll", 30000)
	controlDetail := storageOf(t, control)
	if class(t, controlDetail, classDeclaredOutput).Files != 0 || class(t, controlDetail, classLikelyOutput).Files != 1 {
		t.Fatalf("without an owner policy the file must only be likely output: %+v", controlDetail.Account.Classes)
	}
}

// Measuring changes nothing: same names, sizes, modification times and
// generation before and after.
func TestStorageReportIsReadOnly(t *testing.T) {
	engine, lane := ledgerLane(t, map[string]string{"A/Source.cs": "class S {}\n"}, "")
	putFile(t, lane, "A/bin/a.dll", 5000)
	// Files are compared by size and modification time. Directories are compared
	// by name only: NTFS updates a directory's timestamp lazily on its own.
	snapshot := func() []string {
		lines := []string{}
		_ = filepath.WalkDir(lane, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				return nil
			}
			if info.IsDir() {
				lines = append(lines, path+"|dir")
			} else {
				lines = append(lines, path+"|"+info.ModTime().String()+"|"+strings.Repeat("s", int(info.Size()%7)+1))
			}
			return nil
		})
		sort.Strings(lines)
		return lines
	}
	before := snapshot()
	// Control: the snapshot must notice a real change, or the comparison proves nothing.
	putFile(t, lane, "A/bin/a.dll", 5001)
	if strings.Join(before, "\n") == strings.Join(snapshot(), "\n") {
		t.Fatal("the snapshot cannot see a file being rewritten")
	}
	putFile(t, lane, "A/bin/a.dll", 5000)
	before = snapshot()
	first, err := engine.Storage(context.Background(), "daily")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.Storage(context.Background(), "daily"); err != nil {
		t.Fatal(err)
	}
	after := snapshot()
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("the lane changed while being measured:\nbefore %v\nafter  %v", before, after)
	}
	status, err := engine.Status(context.Background(), "daily")
	if err != nil {
		t.Fatal(err)
	}
	if status.Generation != first.Generation {
		t.Fatalf("generation moved from %d to %d", first.Generation, status.Generation)
	}
}

// A shared dependency mapping is listed with its target and never measured: its
// bytes belong to every lane that links it.
func TestStorageDoesNotMeasureASharedMappingTarget(t *testing.T) {
	engine, lane, cache := sharingFixture(t)
	if err := os.WriteFile(filepath.Join(cache, "big.bin"), []byte(strings.Repeat("z", 400000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Share(context.Background(), "daily", "packages", cache, "test-identity", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(lane, "packages")); err != nil {
		t.Fatal(err)
	}
	detail := storageOf(t, engine)
	if detail.Account.Total.LogicalBytes >= 400000 {
		t.Fatalf("the shared target was measured as lane content: %d bytes", detail.Account.Total.LogicalBytes)
	}
	if len(detail.Mappings) != 1 || detail.Mappings[0].Path != "packages" || detail.Mappings[0].Target == "" {
		t.Fatalf("mapping not reported: %+v", detail.Mappings)
	}
	listed := false
	for _, link := range detail.Account.Links {
		listed = listed || link == "packages"
	}
	if !listed {
		t.Fatalf("the mapping was not reported as a link that was not entered: %v", detail.Account.Links)
	}
	if !detail.Mappings[0].SameVolumeAsLanes {
		t.Fatalf("a mapping created beside the lanes must be on the same volume: %+v", detail.Mappings[0])
	}
}

// A lane file that is also a hard link to a file outside the lane is shared, and
// destroying the lane would not free it.
func TestStorageReportsFilesSharedWithTheOutside(t *testing.T) {
	engine, lane := ledgerLane(t, map[string]string{"A/Source.cs": "class S {}\n"}, "")
	store := filepath.Join(filepath.Dir(lane), "external-store")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(store, "pkg.dll")
	if err := os.WriteFile(external, []byte(strings.Repeat("p", 50000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(lane, "A", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(external, filepath.Join(lane, "A", "bin", "pkg.dll")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	putFile(t, lane, "A/bin/own.dll", 20000)
	output := class(t, storageOf(t, engine), classLikelyOutput)
	if output.SharedBytes < 50000 || output.LinkedFiles != 1 {
		t.Fatalf("linked file not reported as shared: %+v", output)
	}
	if output.ReclaimableBytes < 20000 || output.ReclaimableBytes >= output.AllocatedBytes {
		t.Fatalf("only the private file is reclaimable: %+v", output)
	}
}

// An existing centralised .NET output directory is honoured when a tracked
// Directory.Build.props declares it, and ignored when it cannot be placed
// safely inside the lane.
func TestStorageHonoursAnExistingDotnetArtifactsLayout(t *testing.T) {
	// "gen-lane" is not a conventional output name, so only the declaration can
	// make its files output.
	declared := "<Project><PropertyGroup><ArtifactsPath>$(MSBuildThisFileDirectory)gen-lane</ArtifactsPath></PropertyGroup></Project>\n"
	engine, lane := ledgerLane(t, map[string]string{"Directory.Build.props": declared, "A/Source.cs": "class S {}\n"}, "")
	putFile(t, lane, "gen-lane/Debug/A.dll", 9000)
	putFile(t, lane, "A/gen-lane/keep.txt", 100)
	detail := storageOf(t, engine)
	if len(detail.Artifacts) != 1 || detail.Artifacts[0].Directory != "gen-lane" {
		t.Fatalf("layout not detected: %+v", detail.Artifacts)
	}
	if got := class(t, detail, classLikelyOutput); got.Files != 1 || got.LogicalBytes != 9000 {
		t.Fatalf("output under the declared directory = %+v, want only the file in gen-lane/", got)
	}
	if class(t, detail, classOther).Files != 1 {
		t.Fatalf("a same-named directory elsewhere must not be claimed: %+v", detail.Account.Classes)
	}

	// Control: identical files with no declaration are not guessed to be output.
	control, controlLane := ledgerLane(t, map[string]string{"Directory.Build.props": "<Project/>\n", "A/Source.cs": "class S {}\n"}, "")
	putFile(t, controlLane, "gen-lane/Debug/A.dll", 9000)
	controlDetail := storageOf(t, control)
	if class(t, controlDetail, classLikelyOutput).Files != 0 || class(t, controlDetail, classOther).Files != 1 {
		t.Fatalf("without a declaration the file must be other-local: %+v", controlDetail.Account.Classes)
	}

	for name, value := range map[string]string{
		"escapes":   "$(MSBuildThisFileDirectory)../elsewhere",
		"other":     "$(SomeOtherProperty)/out",
		"absolute":  "/etc",
		"expansion": "$(MSBuildThisFileDirectory)$(Configuration)",
	} {
		props := "<Project><PropertyGroup><ArtifactsPath>" + value + "</ArtifactsPath></PropertyGroup></Project>\n"
		unsafeEngine, _ := ledgerLane(t, map[string]string{"Directory.Build.props": props, "A/Source.cs": "class S {}\n"}, "")
		if layouts := storageOf(t, unsafeEngine).Artifacts; len(layouts) != 0 {
			t.Errorf("%s: unsafe ArtifactsPath was accepted: %+v", name, layouts)
		}
	}
}

func TestStorageUnknownLaneFailsWithoutMeasuring(t *testing.T) {
	engine, _ := ledgerLane(t, map[string]string{"A/Source.cs": "class S {}\n"}, "")
	if _, err := engine.Storage(context.Background(), "missing"); err == nil {
		t.Fatal("measured a lane that does not exist")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Storage(ctx, "daily"); err == nil {
		t.Fatal("a cancelled measurement reported success")
	}
}
