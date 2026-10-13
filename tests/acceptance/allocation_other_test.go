//go:build !windows && !linux && !darwin

package acceptance

import "fmt"

func allocatedFileBytes(string) (uint64, error) {
	return 0, fmt.Errorf("allocation measurement unavailable on this platform")
}
