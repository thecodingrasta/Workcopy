package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
)

// UpdateCheckedOutBranch prepares a HEAD update with Git's expected-old-value
// check. Git locks HEAD and its referent during prepare. Check the symbolic
// target while those locks are held, then update the checkout before committing.
func (r *Repository) UpdateCheckedOutBranch(ctx context.Context, branch, base, candidate string, updateCheckout func() error) error {
	if !strings.HasPrefix(branch, "refs/heads/") || !ValidObjectID(base) || !ValidObjectID(candidate) {
		return fmt.Errorf("invalid checked-out branch transaction")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Closing stdin releases prepared locks through Git's normal abort path.
	// Killing Git on context cancellation could leave its lockfiles behind.
	command := r.Command(context.WithoutCancel(ctx), r.Root, "update-ref", "-m", "workcopy: land approved batch", "--stdin")
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		stdin.Close()
		return err
	}
	var diagnostics bytes.Buffer
	command.Stderr = &limitedWriter{Writer: &diagnostics, Remaining: 64 << 10}
	if err = command.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return err
	}
	waited := false
	wait := func() error {
		waited = true
		stdin.Close()
		_, _ = io.Copy(io.Discard, stdout)
		return command.Wait()
	}
	defer func() {
		if !waited {
			_ = wait()
		}
	}()
	reply := bufio.NewReader(stdout)
	send := func(input, expected string) error {
		if _, writeErr := io.WriteString(stdin, input); writeErr != nil {
			_ = wait()
			return fmt.Errorf("send Git ref transaction command: %w: %s", writeErr, strings.TrimSpace(diagnostics.String()))
		}
		line, readErr := reply.ReadString('\n')
		if readErr != nil || strings.TrimSpace(line) != expected {
			_ = wait()
			return fmt.Errorf("Git ref transaction did not report %q: %s", expected, strings.TrimSpace(diagnostics.String()))
		}
		return nil
	}
	if err = send("start\n", "start: ok"); err != nil {
		return err
	}
	if err = send("update HEAD "+candidate+" "+base+"\nprepare\n", "prepare: ok"); err != nil {
		return err
	}
	head, err := r.Run(ctx, r.Root, nil, "symbolic-ref", "--quiet", "--no-recurse", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != branch {
		return fmt.Errorf("HEAD no longer names the approved branch %s", branch)
	}
	refs, err := r.Run(ctx, r.Root, nil, "for-each-ref", "--format=%(refname)%09%(symref)", "--", branch)
	first, _, _ := strings.Cut(string(refs), "\n")
	if err != nil || strings.TrimSuffix(first, "\r") != branch+"\t" {
		return fmt.Errorf("approved branch %s is not a direct ref", branch)
	}
	if err = updateCheckout(); err != nil {
		return err
	}
	// Once the checkout was successfully updated, finish the prepared ref
	// transaction even if the caller cancelled in the meantime.
	if err = send("commit\n", "commit: ok"); err != nil {
		return err
	}
	if err = wait(); err != nil {
		return fmt.Errorf("finish Git ref transaction: %w: %s", err, strings.TrimSpace(diagnostics.String()))
	}
	return nil
}
