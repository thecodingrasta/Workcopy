// Package git delegates repository semantics to the installed Git executable.
package git

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"workcopy/internal/model"
)

type Repository struct{ Root, CommonDir, Executable string }
type TreeEntry struct {
	Mode     uint32
	ObjectID string
	Size     int64
	Kind     string
}

var objectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func ValidObjectID(value string) bool { return objectID.MatchString(value) }

// commitMetadataEnvironment lists the only GIT_* variables Workcopy passes to
// Git. They set who authored a review commit and when, which is how CI jobs and
// containers normally supply an identity. Variables that redirect Git to
// another repository, work tree, index or config (GIT_DIR, GIT_WORK_TREE,
// GIT_INDEX_FILE, GIT_CONFIG_*) stay stripped.
var commitMetadataEnvironment = map[string]bool{
	"GIT_AUTHOR_NAME": true, "GIT_AUTHOR_EMAIL": true, "GIT_AUTHOR_DATE": true,
	"GIT_COMMITTER_NAME": true, "GIT_COMMITTER_EMAIL": true, "GIT_COMMITTER_DATE": true,
}

func Environment() []string {
	result := []string{}
	for _, value := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
		if !strings.HasPrefix(key, "GIT_") || commitMetadataEnvironment[key] {
			result = append(result, value)
		}
	}
	return append(result, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_CONFIG_NOSYSTEM=1")
}

// RunUnforced runs a read-only Git command with the repository's own
// configuration and none of the overrides Workcopy applies to itself, for
// questions about how the user's Git is set up.
func (r *Repository) RunUnforced(ctx context.Context, directory string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, r.Executable, append([]string{"--no-pager", "-C", directory}, args...)...)
	command.Env = Environment()
	return command.Output()
}

func (r *Repository) Command(ctx context.Context, directory string, args ...string) *exec.Cmd {
	prefix := []string{"--no-pager"}
	if runtime.GOOS == "windows" {
		// Git for Windows refuses paths of 260 characters or more unless told
		// otherwise, however the operating system is configured. Review
		// worktrees sit deep inside a lane root, so Workcopy's own Git commands
		// always allow them. It does not touch the repository's configuration.
		prefix = append(prefix, "-c", "core.longpaths=true")
	}
	prefix = append(prefix,
		"-c", "core.hooksPath="+os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "core.autocrlf=false",
		"-c", "core.safecrlf=false",
		"-c", "core.attributesFile="+os.DevNull,
		"-c", "core.excludesFile="+os.DevNull,
		"-c", "submodule.recurse=false",
		"-c", "protocol.allow=never",
		"-c", "commit.gpgSign=false",
		"-c", "core.quotePath=false",
		"-c", "gc.auto=0",
		"-c", "gc.autoDetach=false",
		"-c", "maintenance.auto=false",
		"-c", "maintenance.autoDetach=false",
		"-C", directory)
	cmd := exec.CommandContext(ctx, r.Executable, append(prefix, args...)...)
	cmd.Env = Environment()
	return cmd
}
func (r *Repository) Run(ctx context.Context, directory string, input []byte, args ...string) ([]byte, error) {
	return r.RunReader(ctx, directory, bytes.NewReader(input), args...)
}

// RunReader is Run with standard input streamed from a reader, for content too
// large to hold in memory.
func (r *Repository) RunReader(ctx context.Context, directory string, input io.Reader, args ...string) ([]byte, error) {
	command := r.Command(ctx, directory, args...)
	command.Stdin = input
	var output, diagnostics bytes.Buffer
	command.Stdout = &limitedWriter{Writer: &output, Remaining: 64 << 20}
	command.Stderr = &limitedWriter{Writer: &diagnostics, Remaining: 64 << 10}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("git %s failed: %w: %s", args[0], err, strings.TrimSpace(diagnostics.String()))
	}
	return output.Bytes(), nil
}

type limitedWriter struct {
	Writer    io.Writer
	Remaining int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.Remaining {
		return 0, fmt.Errorf("Git output exceeded limit")
	}
	n, err := w.Writer.Write(p)
	w.Remaining -= int64(n)
	return n, err
}
func Open(ctx context.Context, directory string) (*Repository, error) {
	executable, err := Executable()
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	r := &Repository{Executable: executable}
	body, err := r.Run(ctx, absolute, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, r.classifyOpenFailure(ctx, absolute, err)
	}
	r.Root, err = filepath.EvalSymlinks(strings.TrimSpace(string(body)))
	if err != nil {
		return nil, err
	}
	body, err = r.Run(ctx, r.Root, nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	r.CommonDir, err = filepath.EvalSymlinks(strings.TrimSpace(string(body)))
	if err != nil {
		return nil, err
	}
	return r, nil
}

// classifyOpenFailure turns Git's reason for not opening a work tree into a
// blocked-precondition error that says what to do. The cause is preserved in
// the message; nothing here changes Git or repository configuration.
func (r *Repository) classifyOpenFailure(ctx context.Context, directory string, cause error) error {
	if ctx.Err() != nil {
		return cause
	}
	if strings.Contains(cause.Error(), "dubious ownership") {
		return model.Fail(3, "git-unsafe-directory", "Git refuses this repository because it is owned by a different account than the one running Workcopy ("+directory+"). This is common when WSL, a container or another user touches a Windows-owned checkout. Run Workcopy as the owner, or add the directory to Git's safe.directory list yourself (git config --global --add safe.directory <path>); Workcopy never edits Git configuration.")
	}
	if body, err := r.Run(ctx, directory, nil, "rev-parse", "--is-bare-repository"); err == nil && strings.TrimSpace(string(body)) == "true" {
		return model.Fail(3, "bare-repository", directory+" is a bare Git repository. Lanes are cut from a repository with a working tree: run Workcopy from a normal clone, or pass --repo for one.")
	}
	if _, err := r.Run(ctx, directory, nil, "rev-parse", "--git-dir"); err != nil {
		return model.Fail(3, "not-a-repository", directory+" is not inside a Git repository. Run Workcopy from a checkout, or pass --repo DIRECTORY.")
	}
	return cause
}

func (r *Repository) Resolve(ctx context.Context, ref string) (string, string, error) {
	requested := ref
	if ref == "" {
		body, err := r.Run(ctx, r.Root, nil, "symbolic-ref", "-q", "HEAD")
		if err == nil {
			ref = strings.TrimSpace(string(body))
		} else {
			ref = "HEAD"
		}
	}
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\r\n") {
		return "", "", model.Fail(2, "invalid-ref", "Invalid Git reference")
	}
	body, err := r.Run(ctx, r.Root, nil, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		if ctx.Err() != nil {
			return "", "", err
		}
		if requested == "" {
			if _, headErr := r.Run(ctx, r.Root, nil, "rev-parse", "--verify", "--quiet", "HEAD"); headErr != nil {
				return "", "", model.Fail(3, "no-commits", "This repository has no commits yet. Make an initial commit, then create a lane.")
			}
		}
		return "", "", model.Fail(2, "ref-not-found", "Git reference does not name a commit in this repository: "+requested)
	}
	commit := strings.TrimSpace(string(body))
	if !ValidObjectID(commit) {
		return "", "", fmt.Errorf("Git returned an invalid commit ID")
	}
	if ref == "HEAD" {
		return commit, commit, nil
	}
	// Preserve a full ref so branch/tag ambiguity cannot redirect a future sync.
	symbolic, symbolErr := r.Run(ctx, r.Root, nil, "rev-parse", "--symbolic-full-name", "--verify", "--end-of-options", ref)
	if symbolErr == nil && strings.HasPrefix(strings.TrimSpace(string(symbolic)), "refs/") {
		ref = strings.TrimSpace(string(symbolic))
	} else {
		ref = commit
	}
	return ref, commit, nil
}
func (r *Repository) Tree(ctx context.Context, commit string) (map[string]TreeEntry, error) {
	if !ValidObjectID(commit) {
		return nil, fmt.Errorf("invalid commit ID")
	}
	body, err := r.Run(ctx, r.Root, nil, "ls-tree", "-r", "-z", "-l", commit)
	if err != nil {
		return nil, err
	}
	result := make(map[string]TreeEntry)
	for _, record := range bytes.Split(body, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		header, name, ok := strings.Cut(string(record), "\t")
		if !ok {
			return nil, fmt.Errorf("invalid Git tree record")
		}
		fields := strings.Fields(header)
		if len(fields) != 4 {
			return nil, fmt.Errorf("invalid Git tree header")
		}
		mode, err := strconv.ParseUint(fields[0], 8, 32)
		if err != nil {
			return nil, err
		}
		size := int64(0)
		if fields[3] != "-" {
			size, err = strconv.ParseInt(fields[3], 10, 64)
			if err != nil {
				return nil, err
			}
		}
		result[name] = TreeEntry{uint32(mode), fields[2], size, fields[1]}
		if len(result) > 100000 {
			return nil, model.Fail(3, "tree-too-large", "Repository has more than 100,000 tracked paths")
		}
	}
	return result, nil
}

type Reader struct {
	command     *exec.Cmd
	input       io.WriteCloser
	outputPipe  io.ReadCloser
	output      *bufio.Reader
	diagnostics bytes.Buffer
	closed      bool
	failed      bool
}

func (r *Repository) Reader(ctx context.Context) (*Reader, error) {
	command := r.Command(ctx, r.Root, "cat-file", "--batch")
	reader := &Reader{command: command}
	var err error
	reader.input, err = command.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	reader.outputPipe = out
	reader.output = bufio.NewReader(out)
	command.Stderr = &limitedWriter{Writer: &reader.diagnostics, Remaining: 64 << 10}
	if err = command.Start(); err != nil {
		return nil, err
	}
	return reader, nil
}

// request asks Git for one object and returns its size once the header has been
// read. The caller must consume exactly that many bytes plus the terminator
// (finish), or mark the reader failed: the pipe is shared by every request.
func (r *Reader) request(id string) (int64, error) {
	if !ValidObjectID(id) {
		return 0, fmt.Errorf("invalid blob ID")
	}
	if r.closed || r.failed {
		return 0, fmt.Errorf("blob reader closed or failed")
	}
	if _, err := fmt.Fprintln(r.input, id); err != nil {
		return 0, err
	}
	header, err := r.output.ReadString('\n')
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != id || fields[1] != "blob" {
		return 0, fmt.Errorf("object is unavailable or not a blob: %s", id)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return 0, fmt.Errorf("invalid blob size from Git")
	}
	if size > model.MaxFileBytes() {
		return 0, fmt.Errorf("blob is %d bytes, over the %d MiB file limit (WORKCOPY_MAX_FILE_MIB)", size, model.MaxFileBytes()>>20)
	}
	return size, nil
}

// finish consumes the newline Git writes after every object.
func (r *Reader) finish() error {
	terminator, err := r.output.ReadByte()
	if err != nil {
		return err
	}
	if terminator != '\n' {
		return fmt.Errorf("invalid Git batch terminator")
	}
	return nil
}

// Read returns a blob held in memory. A blob above the in-memory threshold is
// refused: callers route those through Stream or HashStream.
func (r *Reader) Read(id string) (data []byte, result error) {
	defer func() {
		if result != nil {
			r.failed = true
		}
	}()
	size, err := r.request(id)
	if err != nil {
		return nil, err
	}
	if size > model.InlineFileBytes() {
		return nil, fmt.Errorf("blob is %d bytes, over the %d MiB in-memory threshold; stream it", size, model.InlineFileBytes()>>20)
	}
	body := make([]byte, int(size))
	if _, err = io.ReadFull(r.output, body); err != nil {
		return nil, err
	}
	if err = r.finish(); err != nil {
		return nil, err
	}
	return body, nil
}

// Stream copies a blob of any size into w without holding it, and returns its
// length. If w fails part way the reader is marked failed, because the unread
// remainder is still in the pipe.
func (r *Reader) Stream(id string, w io.Writer) (written int64, result error) {
	defer func() {
		if result != nil {
			r.failed = true
		}
	}()
	size, err := r.request(id)
	if err != nil {
		return 0, err
	}
	written, err = io.CopyBuffer(w, io.LimitReader(r.output, size), make([]byte, 1<<20))
	if err != nil {
		return written, err
	}
	if written != size {
		return written, io.ErrUnexpectedEOF
	}
	return written, r.finish()
}

// HashStream returns the SHA-256 and length of a blob without keeping it.
func (r *Reader) HashStream(id string) (string, int64, error) {
	hasher := sha256.New()
	size, err := r.Stream(id, hasher)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}
func (r *Reader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	r.input.Close()
	// An unread rejected object can fill stdout and block Git forever.
	if r.failed {
		_ = r.command.Process.Kill()
	}
	// On Windows, Git may leave a child process holding the pipe open after
	// the parent is killed. Closing our read end lets os/exec's copy goroutine
	// finish so Wait cannot block on that inherited handle.
	_ = r.outputPipe.Close()
	return r.command.Wait()
}
func (r *Repository) Pin(ctx context.Context, id, commit string) error {
	if !ValidObjectID(commit) || !model.ValidID(id) {
		return fmt.Errorf("invalid pin identity")
	}
	_, err := r.Run(ctx, r.Root, nil, "update-ref", "refs/workcopy/"+id+"/"+commit, commit)
	return err
}
func (r *Repository) Unpin(ctx context.Context, id string) error {
	if !model.ValidID(id) {
		return fmt.Errorf("invalid pin identity")
	}
	body, err := r.Run(ctx, r.Root, nil, "for-each-ref", "--format=%(refname) %(objectname)", "refs/workcopy/"+id+"/")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return fmt.Errorf("invalid pin record")
		}
		if _, err = r.Run(ctx, r.Root, nil, "update-ref", "-d", fields[0], fields[1]); err != nil {
			return err
		}
	}
	return nil
}
