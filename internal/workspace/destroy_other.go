//go:build !windows

package workspace

func leaveDestroyingLane(path, workRoot string) error { return nil }
