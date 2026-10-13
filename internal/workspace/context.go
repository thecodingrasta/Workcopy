package workspace

import (
	"context"
	"workcopy/internal/model"
)

// Inspect returns verified lane metadata while holding the writer lock. It
// does not trust an arbitrary caller-supplied manifest path.
func (e *Engine) Inspect(ctx context.Context, name string) (model.Manifest, error) {
	if err := ctx.Err(); err != nil {
		return model.Manifest{}, err
	}
	opened, err := e.openLane(name)
	if err != nil {
		return model.Manifest{}, err
	}
	defer opened.close()
	lock, err := e.reloadLocked(opened)
	if err != nil {
		return model.Manifest{}, err
	}
	defer lock.Close()
	return opened.snapshot.Manifest, nil
}
