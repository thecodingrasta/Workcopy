//go:build windows

package state

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
)

func TestWindowsCrossProcessLockReportsWorkspaceBusy(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "lock")
	child := exec.Command(os.Args[0], "-test.run=TestWindowsCrossProcessLockHelper", "--", lockPath)
	child.Env = append(os.Environ(), "WORKCOPY_LOCK_HELPER=1")
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || ready != "ready\n" {
		t.Fatalf("lock helper did not become ready: %q: %v", ready, err)
	}

	root, err := fsops.OpenRoot(filepath.Dir(lockPath))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	_, err = Acquire(root, "lock")
	var failure *model.Failure
	if !errors.As(err, &failure) || failure.Exit != 3 || failure.Code != "workspace-busy" {
		t.Fatalf("Acquire error = %v, want workspace-busy failure", err)
	}
	_, _ = stdin.Write([]byte("release\n"))
}

func TestWindowsLockDoesNotBlockLockFileRead(t *testing.T) {
	root, err := fsops.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lock, err := Acquire(root, "lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = root.ReadFile("lock"); err != nil {
		t.Fatalf("reading lock file while owning lock: %v", err)
	}
}

func TestWindowsLockCanBeReacquiredAfterRelease(t *testing.T) {
	root, err := fsops.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	first, err := Acquire(root, "lock")
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatalf("release first lock: %v", err)
	}

	second, err := Acquire(root, "lock")
	if err != nil {
		t.Fatalf("reacquire existing lock file: %v", err)
	}
	if err = second.Close(); err != nil {
		t.Fatalf("release second lock: %v", err)
	}
}

func TestWindowsCrossProcessLockHelper(t *testing.T) {
	if os.Getenv("WORKCOPY_LOCK_HELPER") != "1" {
		return
	}
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		panic(fmt.Sprintf("missing lock path: %v", os.Args))
	}
	file, err := os.OpenFile(os.Args[len(os.Args)-1], os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		panic(err)
	}
	if err = lockFile(file); err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stdout, "ready")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	_ = unlockFile(file)
	_ = file.Close()
	os.Exit(0)
}
