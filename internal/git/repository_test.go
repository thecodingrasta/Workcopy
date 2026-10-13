package git

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"workcopy/internal/testutil"
)

func TestReadsCommitInsteadOfDirtyCheckout(t *testing.T) {
	f := testutil.NewRepository(t)
	f.Write("file", "committed")
	commit := f.Commit("initial")
	f.Write("file", "dirty")
	r, err := Open(context.Background(), f.Path)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := r.Tree(context.Background(), commit)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := r.Reader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	body, err := reader.Read(tree["file"].ObjectID)
	if err != nil || string(body) != "committed" {
		t.Fatal(string(body), err)
	}
}
func TestOptionLikeRefCannotBecomeGitOption(t *testing.T) {
	f := testutil.NewRepository(t)
	f.Write("file", "x")
	f.Commit("initial")
	r, _ := Open(context.Background(), f.Path)
	if _, _, err := r.Resolve(context.Background(), "--help"); err == nil {
		t.Fatal("accepted option as ref")
	}
}

func TestOversizedBlobCloseDoesNotWaitForCancellation(t *testing.T) {
	f := testutil.NewRepository(t)
	id := writeZeroBlob(t, f.Path, (64<<20)+1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	repository, err := Open(ctx, f.Path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := repository.Reader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Read(id); err == nil {
		t.Fatal("oversized object was accepted")
	}
	started := time.Now()
	reader.Close()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("closing rejected object blocked for %s", elapsed)
	}
}

func writeZeroBlob(t *testing.T, repository string, size int64) string {
	t.Helper()
	command := exec.Command("git", "-C", repository, "hash-object", "-w", "--stdin")
	environment := []string{}
	for _, value := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
		if !strings.HasPrefix(key, "GIT_") {
			environment = append(environment, value)
		}
	}
	command.Env = append(environment, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	zeros := make([]byte, 64<<10)
	remaining := size
	for remaining > 0 {
		length := int64(len(zeros))
		if remaining < length {
			length = remaining
		}
		if _, err = input.Write(zeros[:int(length)]); err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatal(err)
		}
		remaining -= length
	}
	if err = input.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(output)
	waitErr := command.Wait()
	if readErr != nil || waitErr != nil {
		t.Fatalf("git hash-object failed: read=%v wait=%v: %s", readErr, waitErr, diagnostics.String())
	}
	return strings.TrimSpace(string(body))
}

func TestCandidateEnumerationExcludesLiteralManagedDirectories(t *testing.T) {
	fixture := testutil.NewRepository(t)
	fixture.Write("base.txt", "base")
	fixture.Commit("base")
	fixture.Write("deps[cache]/package.json", "external dependency")
	fixture.Write("depsc/package.json", "ordinary source")
	fixture.Write("new.txt", "new source")
	repository, err := Open(context.Background(), fixture.Path)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := repository.Candidates(context.Background(), fixture.Path, "deps[cache]")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(candidates, "\n")
	if strings.Contains(got, "deps[cache]") {
		t.Fatal("enumerated excluded dependency directory", got)
	}
	if !strings.Contains(got, "depsc/package.json") || !strings.Contains(got, "new.txt") {
		t.Fatal("literal exclusion hid other source", got)
	}
}
