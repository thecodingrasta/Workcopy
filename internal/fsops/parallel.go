package fsops

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

// WorkersEnvironment overrides the number of concurrent file operations. Set
// it to 1 to make bulk operations strictly sequential when diagnosing a fault.
const WorkersEnvironment = "WORKCOPY_IO_WORKERS"

func workerCount() int {
	if value, err := strconv.Atoi(os.Getenv(WorkersEnvironment)); err == nil && value > 0 {
		return min(value, 64)
	}
	// File operations here wait on the disk, the virus scanner or a 9p round
	// trip rather than the CPU, so a few more workers than cores pays off.
	return min(max(runtime.NumCPU()*2, 4), 16)
}

// Parallel calls fn(0) .. fn(count-1) on a bounded pool. It stops handing out
// work after the first error or cancellation and returns that error. fn must
// only touch state owned by its own index or guard shared state itself.
func Parallel(ctx context.Context, count int, fn func(index int) error) error {
	workers := min(workerCount(), count)
	if workers < 2 || count < 16 {
		for index := 0; index < count; index++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := fn(index); err != nil {
				return err
			}
		}
		return nil
	}
	var (
		next    atomic.Int64
		failed  atomic.Bool
		once    sync.Once
		first   error
		running sync.WaitGroup
	)
	for worker := 0; worker < workers; worker++ {
		running.Add(1)
		go func() {
			defer running.Done()
			for !failed.Load() && ctx.Err() == nil {
				index := int(next.Add(1) - 1)
				if index >= count {
					return
				}
				if err := fn(index); err != nil {
					once.Do(func() { first = err })
					failed.Store(true)
					return
				}
			}
		}()
	}
	running.Wait()
	if first != nil {
		return first
	}
	return ctx.Err()
}
