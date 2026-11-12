//go:build !windows && !linux && !darwin

package storage

import "errors"

// Facts is unavailable on this platform, so every file counts as unreadable and
// the totals say so instead of guessing.
func Facts(string) (FileFacts, error) {
	return FileFacts{}, errors.ErrUnsupported
}
