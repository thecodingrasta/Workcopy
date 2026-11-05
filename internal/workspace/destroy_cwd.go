package workspace

import (
	"os"
	"path/filepath"
)

// leaveDestroyingLane moves the process out of the lane before it is renamed
// into quarantine. Windows does not allow a process to rename its current
// directory, and neither does a Windows drive seen through WSL's drvfs (the
// rename reports success but the new name does not resolve). The CLI commonly
// runs with its working directory inside the lane being destroyed. Leaving is
// harmless elsewhere, so it is not made conditional on the platform.
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
