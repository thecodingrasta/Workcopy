package mcpserver

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"workcopy/internal/settings"
)

// projectSettings layers a repository's own settings file (in its Git directory)
// over the server's settings. It returns base itself when the repository has no
// such file, so callers can tell whether anything changed.
func projectSettings(base *settings.Resolved, commonGitDir string) (*settings.Resolved, error) {
	if base == nil {
		base = settings.Builtin()
	}
	file := settings.ProjectPath(commonGitDir)
	body, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return base, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the repository settings %s: %w", file, err)
	}
	return base.WithProject(file, body)
}
