package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"workcopy/internal/model"
	"workcopy/internal/settings"
	"workcopy/internal/workspace"
)

// showSettings prints every effective setting and where it came from, or with
// --init writes a commented starting file.
func showSettings(ctx context.Context, a arguments, resolved *settings.Resolved) (workspace.Report, error) {
	report := workspace.Report{Command: "settings"}
	if len(a.positionals) != 0 {
		return report, usage(errUnexpected("settings"))
	}
	if a.boolean("project-file") && !a.boolean("init") {
		return report, usage(errors.New("--project-file only goes with --init; plain `workcopy settings` already shows the repository file when there is one"))
	}
	// The repository, when there is one: its settings file takes part. Outside a
	// repository the user-level view is still the right answer.
	projectFile := ""
	found, locateErr := locateTarget(a, resolved)
	if locateErr == nil {
		if engine, newErr := workspace.New(ctx, found.repository, found.root); newErr == nil {
			projectFile = settings.ProjectPath(engine.Repository.CommonDir)
		} else if a.boolean("project-file") {
			return report, newErr
		}
	} else if a.boolean("project-file") {
		return report, locateErr
	}
	if a.boolean("project-file") && projectFile == "" {
		return report, model.Fail(2, "repository-required", "Run inside a Git repository or pass --repo to name the repository these settings belong to")
	}
	if a.boolean("init") {
		target, template := resolved.Candidate, settings.Template()
		if a.boolean("project-file") {
			target, template = projectFile, settings.ProjectTemplate()
		}
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
		if err := os.WriteFile(target, []byte(template), 0600); err != nil {
			return report, err
		}
		report.Message = "Wrote " + target + ". Every setting in it is commented out; uncomment and edit the ones you want."
		return report, nil
	}
	if projectFile != "" {
		body, readErr := os.ReadFile(projectFile)
		switch {
		case readErr == nil:
			layered, layerErr := resolved.WithProject(projectFile, body)
			if layerErr != nil {
				return report, layerErr
			}
			resolved = layered
		case !errors.Is(readErr, fs.ErrNotExist):
			return report, readErr
		}
	}
	report.Settings = resolved.Entries()
	switch {
	case resolved.ProjectFile != "" && resolved.File != "":
		report.Message = "Settings files: " + resolved.ProjectFile + " (this repository), " + resolved.File + " (you)"
	case resolved.ProjectFile != "":
		report.Message = "Settings file: " + resolved.ProjectFile + " (this repository)"
	case resolved.File != "":
		report.Message = "Settings file: " + resolved.File + ". `workcopy settings --init --project-file` creates one for this repository only"
	case resolved.Candidate != "":
		report.Message = "No settings file yet. `workcopy settings --init` creates " + resolved.Candidate
	}
	return report, nil
}

func errUnexpected(command string) error {
	return &model.Failure{Exit: 2, Code: "invalid-arguments", Message: command + " takes no positional arguments"}
}
