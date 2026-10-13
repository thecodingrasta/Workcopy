package project

import (
	"path"
	"strings"
	"testing"
)

func FuzzSingleSegmentGlobAgreesWithPathMatch(f *testing.F) {
	f.Add("*.go", "main.go")
	f.Add("file?.txt", "file1.txt")
	f.Add("[ab]*", "beta")
	f.Add("*", "")
	f.Fuzz(func(t *testing.T, pattern, name string) {
		if pattern == "." || pattern == "**" || strings.ContainsAny(pattern+name, "/\\") {
			return
		}
		expected, err := path.Match(pattern, name)
		if err != nil {
			return
		}
		if got := Match(pattern, name); got != expected {
			t.Fatalf("glob %q name %q: got %v want %v", pattern, name, got, expected)
		}
	})
}
