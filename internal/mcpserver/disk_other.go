//go:build !windows && !linux && !darwin

package mcpserver

import "fmt"

func freeSpace(string) (uint64, error) {
	return 0, fmt.Errorf("MCP storage preflight requires Windows, Linux or macOS")
}
