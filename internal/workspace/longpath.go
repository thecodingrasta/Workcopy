package workspace

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"workcopy/internal/model"
)

// maxPathWithoutLongPaths is the longest full path, in characters, that Git for
// Windows handles unless core.longpaths is on. It refuses 260 and above.
const maxPathWithoutLongPaths = 259

// longestPath returns the longest file name among names and the length of its
// full path under directory, counting the separator as one character.
func longestPath(directory string, names []string) (string, int) {
	longest, length := "", len(directory)
	for _, name := range names {
		if candidate := len(directory) + 1 + len(name); candidate > length {
			longest, length = name, candidate
		}
	}
	return longest, length
}

// longPathAdvice is the warning for a directory whose files Git for Windows,
// run by the user, would refuse; it is "" when nothing is wrong. It never fires
// off Windows, where the limit does not exist, nor once the user's Git is set
// for long paths. The fix is a setting on the repository, so that is what the
// command changes; Workcopy never edits it for the user.
func longPathAdvice(goos, directory, repository string, names []string, longPathsEnabled bool) string {
	if goos != "windows" || longPathsEnabled {
		return ""
	}
	name, length := longestPath(directory, names)
	if length <= maxPathWithoutLongPaths {
		return ""
	}
	return fmt.Sprintf("Git for Windows cannot read paths of 260 characters or more unless core.longpaths is on. %s would be %d characters long, so running git status in %s reports files you did not touch as modified (\"Filename too long\"). Run: git -C %s config core.longpaths true. Workcopy's own Git commands already allow long paths.", name, length, directory, shellQuote(repository))
}

// userLongPaths reports whether the repository's own Git configuration has
// core.longpaths on. Workcopy forces it on for its own commands, so this asks
// Git directly, with the same lookup the user's Git would make.
func (e *Engine) userLongPaths(ctx context.Context) bool {
	if runtime.GOOS != "windows" {
		return true
	}
	body, err := e.Repository.RunUnforced(ctx, e.Repository.Root, "config", "--type=bool", "--get", "core.longpaths")
	return err == nil && strings.TrimSpace(string(body)) == "true"
}

// typicalReviewDirectory stands in for a review's directory when estimating
// before any review exists: the reviews folder, a 24-character lane name and the
// short ID suffix.
const typicalReviewDirectory = `\.workcopy-reviews\` + "123456789012345678901234-12345678"

// doctorLongPathWarning estimates, before the first review, whether a tracked
// path will be too long for the user's Git for Windows once it sits in a review
// worktree under this repository's lane root.
func (e *Engine) doctorLongPathWarning(ctx context.Context) string {
	if runtime.GOOS != "windows" || e.userLongPaths(ctx) {
		return ""
	}
	_, commit, err := e.Repository.Resolve(ctx, "")
	if err != nil {
		return ""
	}
	tree, err := e.Repository.Tree(ctx, commit)
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	advice := longPathAdvice(runtime.GOOS, e.WorkRoot+typicalReviewDirectory, e.Repository.Root, names, false)
	if advice == "" {
		return ""
	}
	return "Estimate for a typical review: " + advice
}

// reviewLongPathWarning inspects a prepared review for paths the user's Git for
// Windows would refuse, and returns the one-line fix, or "".
func (e *Engine) reviewLongPathWarning(ctx context.Context, receipt model.Review) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	names := make([]string, 0, len(receipt.Files))
	for name := range receipt.Files {
		names = append(names, name)
	}
	if _, length := longestPath(receipt.Path, names); length <= maxPathWithoutLongPaths {
		return ""
	}
	return longPathAdvice(runtime.GOOS, receipt.Path, e.Repository.Root, names, e.userLongPaths(ctx))
}
