package workspace

import "context"

// ProgressFunc receives coarse progress for operations that touch every file in
// a repository (lane creation, review preparation). done and total count files.
// It is called from the operation's own goroutine, so it needs no locking, and
// it must return quickly.
type ProgressFunc func(message string, done, total int)

type progressKey struct{}

// WithProgress attaches a progress receiver to ctx.
func WithProgress(ctx context.Context, report ProgressFunc) context.Context {
	return context.WithValue(ctx, progressKey{}, report)
}

func reportProgress(ctx context.Context, message string, done, total int) {
	if report, ok := ctx.Value(progressKey{}).(ProgressFunc); ok && report != nil {
		report(message, done, total)
	}
}
