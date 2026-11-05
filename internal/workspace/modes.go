package workspace

import (
	"context"
	"runtime"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
)

// executableBitsRespected decides at creation whether a lane tracks POSIX
// execute bits. It must not on Windows, when the repository declares
// core.fileMode=false (Git itself then ignores mode changes), or when the
// storage filesystem reports every file as executable (WSL drvfs/9p, FAT).
func (e *Engine) executableBitsRespected(ctx context.Context, registry *fsops.Root) (bool, error) {
	if runtime.GOOS == "windows" {
		return false, nil
	}
	config, err := e.Repository.CheckoutConfig(ctx)
	if err != nil {
		return false, err
	}
	if !config.FileMode {
		return false, nil
	}
	id, err := model.ID()
	if err != nil {
		return false, err
	}
	probe := ".workcopy-registry/mode-probe-" + id
	if err = registry.WriteAtomic(probe, []byte("probe\n"), 0600); err != nil {
		return false, err
	}
	defer registry.Remove(probe)
	faithful, known := registry.ExecutableBitsFaithful(probe)
	return faithful || !known, nil
}
