package workspace

import (
	"context"
	"io"
	"sync"

	"workcopy/internal/git"
)

// gitObjects serves file contents from the source repository to transactions,
// so they are written straight from Git instead of being staged a second time
// in the lane's object store. One `git cat-file --batch` process answers all
// requests; it is started on first use and stopped when the lane is closed.
//
// Content stays recoverable: every accepted commit is pinned under
// refs/workcopy before a transaction that depends on it is journalled, and the
// transaction re-checks each object against the hash recorded in its journal.
type gitObjects struct {
	repository *git.Repository
	ctx        context.Context

	mu     sync.Mutex
	reader *git.Reader
	err    error
	// progress, when set, is told how many objects have been read.
	progress func(done int)
	reads    int
}

func newGitObjects(ctx context.Context, repository *git.Repository) *gitObjects {
	return &gitObjects{repository: repository, ctx: ctx}
}

// Read implements state.ObjectSource. It stops at once if the operation's
// context was cancelled, which leaves the journal in place for recovery.
func (g *gitObjects) Read(objectID string) ([]byte, error) {
	if err := g.ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return nil, g.err
	}
	if g.reader == nil {
		g.reader, g.err = g.repository.Reader(g.ctx)
		if g.err != nil {
			return nil, g.err
		}
	}
	body, err := g.reader.Read(objectID)
	if err != nil {
		g.err = err
		return nil, err
	}
	g.reads++
	if g.progress != nil && g.reads%256 == 0 {
		g.progress(g.reads)
	}
	return body, nil
}

// Stream implements state.ObjectSource for objects too large to hold. The
// shared Git pipe is held for the whole copy, so one large object is written at
// a time; the memory cost is one fixed buffer however large the file is.
func (g *gitObjects) Stream(objectID string, w io.Writer) (int64, error) {
	if err := g.ctx.Err(); err != nil {
		return 0, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return 0, g.err
	}
	if g.reader == nil {
		g.reader, g.err = g.repository.Reader(g.ctx)
		if g.err != nil {
			return 0, g.err
		}
	}
	size, err := g.reader.Stream(objectID, &contextWriter{ctx: g.ctx, w: w})
	if err != nil {
		g.err = err
		return size, err
	}
	g.reads++
	return size, nil
}

// contextWriter stops a long copy when the operation is cancelled, so a deadline
// is honoured between buffers and not only between files.
type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (c *contextWriter) Write(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.w.Write(p)
}

// Close stops the Git process.
func (g *gitObjects) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reader == nil {
		return nil
	}
	err := g.reader.Close()
	g.reader = nil
	return err
}
