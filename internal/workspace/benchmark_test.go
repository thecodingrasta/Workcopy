package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"workcopy/internal/testutil"
)

func BenchmarkExpandSource(b *testing.B) {
	repository := testutil.NewRepository(b)
	repository.Write("initial/source.txt", "base\n")
	const count = 128
	const bytesPerFile = 64 * 1024
	for n := 0; n < count; n++ {
		repository.Write(fmt.Sprintf("extra/source-%03d.txt", n), fmt.Sprintf("%03d\n", n)+strings.Repeat("x", bytesPerFile-4))
	}
	repository.Commit("synthetic benchmark")
	engine, err := New(context.Background(), repository.Path, filepath.Join(repository.Parent, "lanes"))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(count * bytesPerFile)
	b.ResetTimer()
	b.StopTimer()
	for n := 0; n < b.N; n++ {
		name := fmt.Sprintf("bench-%d", n)
		if _, err = engine.Create(context.Background(), name, CreateOptions{Projects: []string{"initial"}}, false); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		_, err = engine.Expand(context.Background(), name, []string{"extra"}, false)
		b.StopTimer()
		if err != nil {
			b.Fatal(err)
		}
		if _, err = engine.Destroy(context.Background(), name, false, false); err != nil {
			b.Fatal(err)
		}
	}
}
