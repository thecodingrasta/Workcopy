package workspace

import (
	"context"
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
