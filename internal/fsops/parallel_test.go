package fsops

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestParallelVisitsEveryIndexOnceAndReportsTheFirstError(t *testing.T) {
	for _, workers := range []string{"1", "3", "16"} {
		t.Setenv(WorkersEnvironment, workers)
		seen := make([]atomic.Int32, 200)
		if err := Parallel(context.Background(), len(seen), func(i int) error { seen[i].Add(1); return nil }); err != nil {
			t.Fatal(err)
		}
		for i := range seen {
			if seen[i].Load() != 1 {
				t.Fatalf("workers=%s: index %d visited %d times", workers, i, seen[i].Load())
			}
		}
		boom := errors.New("boom")
		var calls atomic.Int32
		err := Parallel(context.Background(), 1000, func(i int) error {
			calls.Add(1)
			if i == 7 {
				return boom
			}
			return nil
		})
		if !errors.Is(err, boom) {
			t.Fatalf("workers=%s: error not returned: %v", workers, err)
		}
		if workers == "1" && calls.Load() != 8 {
			t.Fatalf("sequential mode must stop at the failure, ran %d calls", calls.Load())
		}
	}
}

func TestParallelHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	err := Parallel(ctx, 100000, func(i int) error {
		if calls.Add(1) == 50 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || calls.Load() > 5000 {
		t.Fatalf("cancellation must stop the pool promptly: err=%v calls=%d", err, calls.Load())
	}
}
