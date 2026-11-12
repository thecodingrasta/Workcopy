package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"workcopy/internal/settings"
	"workcopy/internal/workspace"
)

// applyProjectSettings layers the repository's own settings file (in its Git
// directory) over the user's, now that the repository is known, and moves to the
// project's lane root when it names one and nothing more specific did.
func applyProjectSettings(ctx context.Context, a arguments, resolved *settings.Resolved, found target, engine *workspace.Engine) (*settings.Resolved, *workspace.Engine, error) {
	file := settings.ProjectPath(engine.Repository.CommonDir)
	body, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return resolved, engine, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read the repository settings %s: %w", file, err)
	}
	layered, err := resolved.WithProject(file, body)
	if err != nil {
		return nil, nil, err
	}
	if err = layered.ExportProcessEnvironment(os.LookupEnv, os.Setenv); err != nil {
		return nil, nil, err
	}
	// An explicit --root, or the lane the command runs in, already decided.
	if a.value("root") == "" && found.current == nil {
		if root := layered.String(settings.Root); root != found.root {
			if engine, err = workspace.New(ctx, found.repository, root); err != nil {
				return nil, nil, err
			}
		}
	}
	return layered, engine, nil
}
