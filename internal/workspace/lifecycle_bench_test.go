package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"workcopy/internal/testutil"
)

// BenchmarkLaneLifecycle times the operations WC-003 and WC-011 reported as
// slow on large repositories. It is skipped unless WORKCOPY_BENCH_FILES is set,
// so the normal suite stays fast:
//
//	WORKCOPY_BENCH_FILES=4000 go test ./internal/workspace -run '^$' -bench LaneLifecycle -benchtime=1x
//
// Point TMPDIR/TEMP at the filesystem under test (ext4, NTFS, a /mnt/c drive).
// WORKCOPY_BENCH_LANE_ROOT places the lanes on a different filesystem from the
// repository, the layout that keeps lane I/O off a slow mount.
func BenchmarkLaneLifecycle(b *testing.B) {
	count, _ := strconv.Atoi(os.Getenv("WORKCOPY_BENCH_FILES"))
	if count == 0 {
		b.Skip("set WORKCOPY_BENCH_FILES to run the large-repository lifecycle benchmark")
	}
	ctx := context.Background()
	r := testutil.NewRepository(b)
	body := strings.Repeat("// representative source line\n", 500)
	for n := 0; n < count; n++ {
		r.Write(fmt.Sprintf("src/p%03d/f%04d.cs", n%80, n), fmt.Sprintf("// %d\n", n)+body)
	}
	r.Commit("synthetic large repository")
	if os.Getenv("WORKCOPY_BENCH_PACK") == "1" {
		// Real repositories are mostly packed; loose objects cost one file read each.
		r.Git("gc", "--quiet", "--prune=now")
	}
	laneRoot := filepath.Join(r.Parent, "lanes")
	if override := os.Getenv("WORKCOPY_BENCH_LANE_ROOT"); override != "" {
		laneRoot = filepath.Join(override, filepath.Base(r.Parent))
	}
	e, err := New(ctx, r.Path, laneRoot)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := fmt.Sprintf("bench%d", i)
		started := time.Now()
		created, createErr := e.Create(ctx, name, CreateOptions{All: true}, false)
		if createErr != nil {
			b.Fatal(createErr)
		}
		b.ReportMetric(time.Since(started).Seconds(), "create-s")
		started = time.Now()
		if _, err = e.Status(ctx, name); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(time.Since(started).Seconds(), "status-s")
		if err = os.WriteFile(filepath.Join(created.Workspace.Path, "src", "p000", "f0000.cs"), []byte("changed\n"), 0644); err != nil {
			b.Fatal(err)
		}
		started = time.Now()
		if _, err = e.Merge(ctx, name, MergeOptions{}, false); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(time.Since(started).Seconds(), "review-s")
	}
}
