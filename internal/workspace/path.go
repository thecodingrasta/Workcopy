package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"workcopy/internal/model"
)

// IsTempDirectory reports whether path is equal to or inside a system or
// process temporary directory.
func IsTempDirectory(path string) (bool, string) {
	if strings.TrimSpace(path) == "" {
		return false, ""
	}
	target, err := canonical(path)
	if err != nil {
		if abs, absErr := filepath.Abs(path); absErr == nil {
			target = filepath.Clean(abs)
		} else {
			return false, ""
		}
	}
	candidates := []string{os.TempDir(), os.Getenv("TEMP"), os.Getenv("TMP"), os.Getenv("TMPDIR")}
	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			candidates = append(candidates, filepath.Join(localAppData, "Temp"))
		}
		if sysRoot := os.Getenv("SystemRoot"); sysRoot != "" {
			candidates = append(candidates, filepath.Join(sysRoot, "Temp"))
		}
		if winDir := os.Getenv("WINDIR"); winDir != "" {
			candidates = append(candidates, filepath.Join(winDir, "Temp"))
		}
	} else {
		candidates = append(candidates, "/tmp", "/var/tmp", "/private/tmp", "/private/var/folders", "/dev/shm")
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		canon, canonErr := canonical(candidate)
		if canonErr != nil {
			if abs, absErr := filepath.Abs(candidate); absErr == nil {
				canon = filepath.Clean(abs)
			} else {
				continue
			}
		}
		if containsPath(canon, target) {
			return true, canon
		}
	}
	if runtime.GOOS == "windows" {
		lower := strings.ToLower(filepath.Clean(target))
		for _, marker := range []string{`\appdata\local\temp`, `\windows\temp`} {
			if strings.HasSuffix(lower, marker) || strings.Contains(lower, marker+`\`) {
				return true, marker
			}
		}
	}
	return false, ""
}

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
