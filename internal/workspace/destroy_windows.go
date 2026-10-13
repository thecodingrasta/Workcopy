//go:build windows

package workspace

import (
	"os"
	"path/filepath"
)

// Windows does not allow a process to rename its current directory. The CLI
// commonly runs with its working directory inside the lane being destroyed.
func leaveDestroyingLane(path, workRoot string) error {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return err
	}
	if containsPath(path, workingDirectory) {
		return os.Chdir(filepath.Clean(workRoot))
	}
	return nil
}
