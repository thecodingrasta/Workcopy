//go:build !windows && !linux && !darwin

package storage

import "errors"

// Describe is unavailable on this platform.
func Describe(string) (Volume, error) { return Volume{}, errors.ErrUnsupported }
