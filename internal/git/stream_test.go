package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"workcopy/internal/model"
	"workcopy/internal/testutil"
)

func sha(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }

func fillBytes(size int, seed uint32) []byte {
	out := make([]byte, size)
	for i := range out {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		out[i] = byte(seed)
	}
	return out
}

func blobIDs(t *testing.T, r *testutil.Repository, contents map[string][]byte) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for name, body := range contents {
		ids[name] = strings.TrimSpace(r.GitInput(string(body), "hash-object", "-w", "--stdin"))
	}
	return ids
}

// The cat-file pipe is shared by every request. A streaming read that stops one
// byte early or late desynchronises it and corrupts the next object, so the
// order of mixed reads is part of the contract.
func TestStreamAndReadShareOnePipeWithoutDesynchronising(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("seed", "x")
	r.Commit("seed")
	contents := map[string][]byte{
		"empty":  {},
		"small":  []byte("small\n"),
		"medium": fillBytes(3<<20+11, 1),
		"large":  fillBytes(5<<20+3, 2),
		"after":  []byte("after the large one\n"),
	}
	ids := blobIDs(t, r, contents)
	repository, err := Open(context.Background(), r.Path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := repository.Reader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	var big bytes.Buffer
	order := []string{"small", "large", "empty", "medium", "after", "large"}
	for _, name := range order {
		switch name {
		case "large", "medium":
			big.Reset()
			size, streamErr := reader.Stream(ids[name], &big)
			if streamErr != nil || size != int64(len(contents[name])) || !bytes.Equal(big.Bytes(), contents[name]) {
				t.Fatalf("stream %s: size %d err %v", name, size, streamErr)
			}
			sum, hashed, hashErr := reader.HashStream(ids[name])
			if hashErr != nil || sum != sha(contents[name]) || hashed != int64(len(contents[name])) {
				t.Fatalf("hash %s: %s %d %v", name, sum, hashed, hashErr)
			}
		default:
			body, readErr := reader.Read(ids[name])
			if readErr != nil || !bytes.Equal(body, contents[name]) {
				t.Fatalf("read %s after a stream: %q %v", name, body, readErr)
			}
		}
	}
}

func TestReadRefusesAboveTheInMemoryThresholdAndStreamStillWorks(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("seed", "x")
	r.Commit("seed")
	data := fillBytes(200_000, 9)
	ids := blobIDs(t, r, map[string][]byte{"f": data})
	defer model.SetInlineFileBytes(100_000)()
	repository, _ := Open(context.Background(), r.Path)
	reader, err := repository.Reader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err = reader.Read(ids["f"]); err == nil || !strings.Contains(err.Error(), "stream it") {
		t.Fatalf("Read must point at Stream above the threshold, got %v", err)
	}
	// Read marks the reader failed because the rejected object was never drained.
	reader2, _ := repository.Reader(context.Background())
	defer reader2.Close()
	var out bytes.Buffer
	if n, streamErr := reader2.Stream(ids["f"], &out); streamErr != nil || n != int64(len(data)) || !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("stream above the threshold: %d %v", n, streamErr)
	}
}

func TestStreamRefusesAboveTheHardLimitWithAnActionableMessage(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("seed", "x")
	r.Commit("seed")
	ids := blobIDs(t, r, map[string][]byte{"f": fillBytes(300_000, 5)})
	defer model.SetMaxFileBytes(100_000)()
	repository, _ := Open(context.Background(), r.Path)
	reader, _ := repository.Reader(context.Background())
	defer reader.Close()
	_, err := reader.Stream(ids["f"], io.Discard)
	if err == nil || !strings.Contains(err.Error(), "WORKCOPY_MAX_FILE_MIB") {
		t.Fatalf("the refusal must name the setting that changes it: %v", err)
	}
}

type failingWriter struct{ after int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.after -= len(p); w.after < 0 {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

// A destination that fails part way leaves the rest of the object in the pipe,
// so the reader must refuse further use instead of serving garbage.
func TestStreamFailureDisablesTheReaderRatherThanCorruptingTheNextObject(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("seed", "x")
	r.Commit("seed")
	ids := blobIDs(t, r, map[string][]byte{"big": fillBytes(4<<20, 3), "next": []byte("next\n")})
	repository, _ := Open(context.Background(), r.Path)
	reader, _ := repository.Reader(context.Background())
	defer reader.Close()
	if _, err := reader.Stream(ids["big"], &failingWriter{after: 1 << 20}); err == nil {
		t.Fatal("a failing destination must fail the stream")
	}
	if body, err := reader.Read(ids["next"]); err == nil {
		t.Fatalf("the reader must refuse to continue after a half-read object, but returned %q", body)
	}
}

func TestRunReaderFeedsGitFromAStreamWithoutBuffering(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("seed", "x")
	r.Commit("seed")
	repository, _ := Open(context.Background(), r.Path)
	data := fillBytes(6<<20+1, 7)
	out, err := repository.RunReader(context.Background(), r.Path, bytes.NewReader(data), "hash-object", "-w", "--no-filters", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(string(out))
	got := r.Git("cat-file", "-s", id)
	if got != "6291457" {
		t.Fatalf("git stored %s bytes, want 6291457", got)
	}
}
