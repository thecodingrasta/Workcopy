package cli

import (
	"os"
	"path/filepath"

	"workcopy/internal/model"
	"workcopy/internal/settings"
	"workcopy/internal/workspace"
)

// showSettings prints every effective setting and where it came from, or with
// --init writes a commented starting file.
func showSettings(a arguments, resolved *settings.Resolved) (workspace.Report, error) {
	report := workspace.Report{Command: "settings"}
	if len(a.positionals) != 0 {
		return report, usage(errUnexpected("settings"))
	}
	if a.boolean("init") {
		target := resolved.Candidate
		if target == "" {
			return report, model.Fail(3, "no-settings-location", "No per-user configuration directory is available; pass --settings FILE")
		}
		if a.boolean("dry-run") {
			report.Message = "Would write a commented settings file to " + target
			return report, nil
		}
		if _, err := os.Lstat(target); err == nil {
			return report, model.Fail(3, "settings-file-exists", target+" already exists; edit it instead")
		} else if !os.IsNotExist(err) {
			return report, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return report, err
		}
		if err := os.WriteFile(target, []byte(settings.Template()), 0600); err != nil {
			return report, err
		}
		report.Message = "Wrote " + target + ". Every setting in it is commented out; uncomment and edit the ones you want."
		return report, nil
	}
	report.Settings = resolved.Entries()
	switch {
	case resolved.File != "":
		report.Message = "Settings file: " + resolved.File
	case resolved.Candidate != "":
		report.Message = "No settings file yet. `workcopy settings --init` creates " + resolved.Candidate
	}
	return report, nil
}

func errUnexpected(command string) error {
	return &model.Failure{Exit: 2, Code: "invalid-arguments", Message: command + " takes no positional arguments"}
}
