package git

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
)

func (r *Repository) RunIndex(ctx context.Context, directory, index string, input []byte, args ...string) ([]byte, error) {
	if !filepath.IsAbs(index) {
		return nil, fmt.Errorf("alternate index must be absolute")
	}
	command := r.Command(ctx, directory, args...)
	command.Env = append(command.Env, "GIT_INDEX_FILE="+index)
	command.Stdin = bytes.NewReader(input)
	var output, diagnostics bytes.Buffer
	command.Stdout = &limitedWriter{Writer: &output, Remaining: 64 << 20}
	command.Stderr = &limitedWriter{Writer: &diagnostics, Remaining: 64 << 10}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("git %s with isolated index: %w: %s", args[0], err, diagnostics.String())
	}
	return output.Bytes(), nil
}
