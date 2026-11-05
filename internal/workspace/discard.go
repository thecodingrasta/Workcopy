package workspace

import (
	"strings"

	"workcopy/internal/model"
	"workcopy/internal/project"
)

const maxDiscardPatterns = 64

// validateDestroyPolicy refuses patterns that could discard a whole lane. The
// list is meant for build output such as **/bin/** or **/obj/**.
func validateDestroyPolicy(policy *model.DestroyPolicy) error {
	if policy == nil {
		return nil
	}
	if len(policy.Discard) > maxDiscardPatterns {
		return model.Fail(2, "invalid-discard-pattern", "destroy.discard allows at most 64 patterns")
	}
	for _, pattern := range policy.Discard {
		trimmed := strings.Trim(pattern, "/")
		if trimmed == "" || trimmed == "." || trimmed == "*" || trimmed == "**" || len(pattern) > 256 || strings.Contains(pattern, "\\") || strings.HasPrefix(pattern, "/") {
			return model.Fail(2, "invalid-discard-pattern", "destroy.discard pattern "+quote(pattern)+" is empty, absolute or matches everything; name the build-output directories instead, for example **/bin/**")
		}
		for _, part := range strings.Split(trimmed, "/") {
			if part == ".." || part == "" {
				return model.Fail(2, "invalid-discard-pattern", "destroy.discard pattern "+quote(pattern)+" must be a clean relative glob")
			}
		}
	}
	return nil
}

func quote(value string) string { return `"` + value + `"` }

// discardable reports whether destroy may drop a local-only file without
// --force. Tracked source is never discardable this way, and neither is
// anything when the configuration came from the repository itself.
func (l *lane) discardable(name string) bool {
	manifest := l.snapshot.Manifest
	if !manifest.OwnerConfig || manifest.Config.Destroy == nil {
		return false
	}
	for _, pattern := range manifest.Config.Destroy.Discard {
		if project.Match(pattern, name) {
			return true
		}
	}
	return false
}
