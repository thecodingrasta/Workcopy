// Package git delegates repository semantics to the installed Git executable.
package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
func Environment() []string {
	result := []string{}
	for _, value := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
		if !strings.HasPrefix(key, "GIT_") {
			result = append(result, value)
		}
	}
	return append(result, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_CONFIG_NOSYSTEM=1")
}
func (r *Repository) Command(ctx context.Context, directory string, args ...string) *exec.Cmd {
	prefix := []string{"--no-pager", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "core.autocrlf=false", "-c", "core.safecrlf=false", "-c", "core.attributesFile=" + os.DevNull, "-c", "core.excludesFile=" + os.DevNull, "-c", "submodule.recurse=false", "-c", "protocol.allow=never", "-c", "commit.gpgSign=false", "-c", "core.quotePath=false", "-C", directory}
	cmd := exec.CommandContext(ctx, r.Executable, append(prefix, args...)...)
	cmd.Env = Environment()
	return cmd
}
func (r *Repository) Run(ctx context.Context, directory string, input []byte, args ...string) ([]byte, error) {
	command := r.Command(ctx, directory, args...)
	command.Stdin = bytes.NewReader(input)
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
	executable, err := exec.LookPath("git")
	if err != nil {
		return nil, model.Fail(3, "git-unavailable", "Install Git and put it on PATH")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	r := &Repository{Executable: executable}
	body, err := r.Run(ctx, absolute, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
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
func (r *Repository) Resolve(ctx context.Context, ref string) (string, string, error) {
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
		return "", "", err
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
func (r *Reader) Read(id string) (data []byte, result error) {
	defer func() {
		if result != nil {
			r.failed = true
		}
	}()
	if !ValidObjectID(id) {
		return nil, fmt.Errorf("invalid blob ID")
	}
	if r.closed || r.failed {
		return nil, fmt.Errorf("blob reader closed or failed")
	}
	if _, err := fmt.Fprintln(r.input, id); err != nil {
		return nil, err
	}
	header, err := r.output.ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != id || fields[1] != "blob" {
		return nil, fmt.Errorf("object is unavailable or not a blob: %s", id)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 || size > model.MaxFileBytes {
		return nil, fmt.Errorf("blob exceeds 64 MiB limit")
	}
	body := make([]byte, int(size)+1)
	if _, err = io.ReadFull(r.output, body); err != nil {
		return nil, err
	}
	if body[len(body)-1] != '\n' {
		return nil, fmt.Errorf("invalid Git batch terminator")
	}
	return body[:len(body)-1], nil
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
