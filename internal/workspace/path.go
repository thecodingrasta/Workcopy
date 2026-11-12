package workspace

import (
	"context"
	"fmt"

	"workcopy/internal/model"
)

// Path reports where a lane lives, from the registry alone: it reads no lane
// content and runs no Git, so it is cheap enough to call from a build script
// before every command.
func (e *Engine) Path(ctx context.Context, name string) (Report, error) {
	report := Report{Command: "path"}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	record, err := e.readRegistration(name)
	if err != nil {
		return report, err
	}
	found := e.listView(record)
	report.Workspace = &found
	if found.Phase != "ready" {
		return report, model.Fail(3, "workspace-not-ready", fmt.Sprintf("Lane %s is %s, so %s may be incomplete or gone. Run `workcopy recover %s`, or `workcopy destroy %s --force` to discard it.", name, found.Phase, found.Path, name, name))
	}
	return report, nil
}
