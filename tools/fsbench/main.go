// Command fsbench times the filesystem calls a lane operation is made of
// (create, write, fsync, stat, read, chmod, rename, mkdir, remove) at 1, 4 and
// 16 workers, so a slow directory can be diagnosed on the machine that has it:
//
//	go run ./tools/fsbench /mnt/c/Users/me/scratch
//	go run ./tools/fsbench ~/scratch
//
// It creates a directory under the path it is given and removes it afterwards.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func run(name string, n, workers int, op func(i int) error) {
	start := time.Now()
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	next := make(chan int, n)
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				if err := op(i); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	status := ""
	if firstErr != nil {
		status = " ERR " + firstErr.Error()
	}
	fmt.Printf("%-34s workers=%-2d %8.3f ms/op  (%6.0f ms total)%s\n", name, workers, float64(elapsed.Microseconds())/1000/float64(n), float64(elapsed.Microseconds())/1000, status)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: fsbench DIRECTORY (a scratch directory is created inside it and removed)")
		os.Exit(2)
	}
	dir := filepath.Join(os.Args[1], fmt.Sprintf("workcopy-fsbench-%d", os.Getpid()))
	n := 500
	body := make([]byte, 4096)
	for i := range body {
		body[i] = 'a'
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	for _, workers := range []int{1, 4, 16} {
		sub := filepath.Join(dir, fmt.Sprintf("w%d", workers))
		_ = os.MkdirAll(sub, 0o755)
		q := func(prefix string, i int) string { return filepath.Join(sub, fmt.Sprintf("%s%d", prefix, i)) }
		fmt.Println("---")
		run("create+write4K+close", n, workers, func(i int) error { return os.WriteFile(q("a", i), body, 0o644) })
		run("create+write4K+fsync+close", n, workers, func(i int) error {
			f, err := os.OpenFile(q("b", i), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			if _, err = f.Write(body); err != nil {
				return err
			}
			if err = f.Sync(); err != nil {
				return err
			}
			return f.Close()
		})
		run("create empty+close", n, workers, func(i int) error {
			f, err := os.OpenFile(q("e", i), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			return f.Close()
		})
		run("lstat", n, workers, func(i int) error { _, err := os.Lstat(q("a", i)); return err })
		run("open+read4K+close", n, workers, func(i int) error { _, err := os.ReadFile(q("a", i)); return err })
		run("chmod", n, workers, func(i int) error { return os.Chmod(q("a", i), 0o600) })
		now := time.Now()
		run("chtimes", n, workers, func(i int) error { return os.Chtimes(q("a", i), now, now) })
		run("rename", n, workers, func(i int) error { return os.Rename(q("a", i), q("r", i)) })
		run("mkdir", n/5, workers, func(i int) error { return os.Mkdir(q("d", i), 0o755) })
		run("remove file", n, workers, func(i int) error { return os.Remove(q("r", i)) })
	}
}
