package testutil

import (
	"os"
	"path/filepath"

	"workcopy/internal/settings"
)

// IsolateSettings keeps a developer's own Workcopy preferences out of tests: it
// removes every Workcopy setting from the process environment and points
// WORKCOPY_SETTINGS at an empty file. Call it from TestMain and defer the result.
func IsolateSettings() func() {
	for _, definition := range settings.Definitions {
		os.Unsetenv(definition.Key)
	}
	directory, err := os.MkdirTemp("", "wc-settings-")
	if err != nil {
		panic(err)
	}
	empty := filepath.Join(directory, "workcopy.env")
	if err = os.WriteFile(empty, nil, 0600); err != nil {
		panic(err)
	}
	os.Setenv(settings.FileEnvironment, empty)
	return func() { os.RemoveAll(directory) }
}
