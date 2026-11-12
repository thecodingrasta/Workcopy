package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/model"
)

func TestLongPathAdviceFiresExactlyAtTheWindowsLimit(t *testing.T) {
	directory := `C:\lanes\.workcopy-reviews\lane-abcd1234` // 38 characters
	pad := func(total int) string { return strings.Repeat("x", total-len(directory)-1) }
	cases := []struct {
		name    string
		goos    string
		path    string
		enabled bool
		warn    bool
	}{
		{"259 characters is allowed", "windows", pad(259), false, false},
		{"260 characters is refused by Git", "windows", pad(260), false, true},
		{"far over", "windows", pad(400), false, true},
		{"fine once core.longpaths is on", "windows", pad(400), true, false},
		{"never on Linux", "linux", pad(400), false, false},
		{"never on macOS", "darwin", pad(400), false, false},
	}
	for _, c := range cases {
		got := longPathAdvice(c.goos, directory, `C:\src\repo`, []string{"short.txt", c.path}, c.enabled)
		if (got != "") != c.warn {
			t.Errorf("%s: warning=%v, want %v: %q", c.name, got != "", c.warn, got)
		}
		if got != "" {
			for _, must := range []string{"core.longpaths", "git -C", "config core.longpaths true", "Workcopy's own Git commands"} {
				if !strings.Contains(got, must) {
					t.Errorf("%s: the warning must contain %q: %s", c.name, must, got)
				}
			}
		}
	}
	if name, length := longestPath(directory, []string{"a", "abc/def", "b"}); name != "abc/def" || length != len(directory)+1+7 {
		t.Errorf("longestPath: %q %d", name, length)
	}
}

// A review directory is owned by the receipt whose ID its name is built from.
// The short form is new; the 32-digit form was written by earlier versions and
// must keep working, since existing lanes hold receipts naming it.
func TestReviewDirectoryNamesProveOwnershipInBothFormats(t *testing.T) {
	r := wideRepository(t, 5)
	ctx := context.Background()
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := model.Snapshot{Manifest: model.Manifest{Name: "task"}}
	id := "0123456789abcdef0123456789abcdef"
	reviews := filepath.Join(e.WorkRoot, ".workcopy-reviews")
	valid := []string{"task-01234567", "task-0123456789ab", "task-" + id}
	for _, name := range valid {
		if err = e.validateReviewPath(snapshot, model.Review{ID: id, Path: filepath.Join(reviews, name)}); err != nil {
			t.Errorf("%s must be accepted: %v", name, err)
		}
	}
	forged := []string{
		"task-ffffffff",           // not a prefix of this receipt's ID
		"task-0123456",            // too short to be a unique prefix
		"other-01234567",          // another lane's name
		"task-01234567/../escape", // traversal
		"task-" + id + "0",        // longer than the ID
	}
	for _, name := range forged {
		if err = e.validateReviewPath(snapshot, model.Review{ID: id, Path: filepath.Join(reviews, name)}); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	if err = e.validateReviewPath(snapshot, model.Review{ID: id, Path: filepath.Join(e.WorkRoot, "elsewhere", "task-01234567")}); err == nil {
		t.Error("a path outside the reviews folder must be rejected")
	}
	if err = e.validateReviewPath(snapshot, model.Review{ID: "not-an-id", Path: filepath.Join(reviews, "task-not-an-id")}); err == nil {
		t.Error("an invalid ID must be rejected")
	}

	pattern := reviewDirectoryName("task")
	for _, name := range valid {
		if !pattern.MatchString(name) {
			t.Errorf("retained-review discovery must recognise %s", name)
		}
	}
	for _, name := range []string{"task-0123456", "task-xyz12345", "other-01234567", "task-01234567-extra"} {
		if pattern.MatchString(name) {
			t.Errorf("retained-review discovery must not claim %s", name)
		}
	}
}

func TestNewReviewsUseTheShortNameAndNeverCollide(t *testing.T) {
	r := wideRepository(t, 6)
	ctx := context.Background()
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(created.Workspace.Path, "src", "p00", "dir0", "file0000.txt"), []byte("edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	review, err := e.Merge(ctx, "task", MergeOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(review.Review.Path)
	if want := "task-" + review.Review.ID[:shortReviewDigits]; base != want {
		t.Errorf("new reviews use the short name: got %s, want %s", base, want)
	}
	// Taking the short name lengthens the next candidate instead of colliding.
	id := "aaaaaaaabbbbbbbbccccccccdddddddd"
	if err = os.MkdirAll(filepath.Join(e.WorkRoot, ".workcopy-reviews", "other-aaaaaaaa"), 0755); err != nil {
		t.Fatal(err)
	}
	got, err := e.newReviewDirectory("other", id)
	if err != nil || got != "other-aaaaaaaabbbb" {
		t.Errorf("a taken short name must lengthen: %q %v", got, err)
	}
	// An existing 32-digit review is still discoverable and valid.
	if err = os.MkdirAll(filepath.Join(e.WorkRoot, ".workcopy-reviews", "legacy-"+id), 0755); err != nil {
		t.Fatal(err)
	}
	found, err := e.retainedReviews(ctx, "legacy")
	if err != nil || len(found) != 1 {
		t.Errorf("a legacy-format review must still be listed: %+v %v", found, err)
	}
}
