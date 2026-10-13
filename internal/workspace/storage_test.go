package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	testfixture "workcopy/internal/testutil"
)

func sharingFixture(t *testing.T) (*Engine, string, string) {
	t.Helper()
	repository := testfixture.NewRepository(t)
	repository.Write("A/A.csproj", "<Project Sdk=\"Microsoft.NET.Sdk\"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>\n")
	repository.Write("A/Source.cs", "class Source {}\n")
	repository.Write(".gitignore", "packages/\n")
	repository.Commit("base")
	engine, err := New(context.Background(), repository.Path, filepath.Join(repository.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := engine.Create(context.Background(), "daily", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(repository.Parent, "package-cache")
	if err = os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(cache, "preserve.txt"), []byte("shared content"), 0600); err != nil {
		t.Fatal(err)
	}
	return engine, created.Workspace.Path, cache
}

func TestExplicitShareAndDestroyNeverDeleteDependencyTarget(t *testing.T) {
	engine, lane, cache := sharingFixture(t)
	_, err := engine.Share(context.Background(), "daily", "packages", cache, "test-lock-and-runtime-identity", true, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(lane, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	if _, linkErr := os.Readlink(filepath.Join(lane, "packages")); linkErr != nil || info.IsDir() {
		t.Fatal("dependency tree was copied instead of linked")
	}
	if _, err = engine.Destroy(context.Background(), "daily", false, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cache, "preserve.txt"))
	if err != nil || string(data) != "shared content" {
		t.Fatalf("shared target changed: %q, %v", data, err)
	}
}

func TestSharedDependencyInputDriftIsVisible(t *testing.T) {
	engine, lane, cache := sharingFixture(t)
	if _, err := engine.Share(context.Background(), "daily", "packages", cache, "test-environment", true, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, "A", "A.csproj"), []byte("<Project Sdk=\"Changed\"/>\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := engine.Status(context.Background(), "daily")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(report.Warnings, "\n"), "stale") {
		t.Fatal("dependency compatibility drift was hidden")
	}
}

func TestShareDryRunDoesNotWriteMetadataOrCreateLink(t *testing.T) {
	engine, lane, cache := sharingFixture(t)
	manifest := filepath.Join(lane, ".workcopy", "manifest.json")
	before, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.Share(context.Background(), "daily", "packages", cache, "test-environment", true, true); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("dry-run changed metadata")
	}
	if _, err = os.Lstat(filepath.Join(lane, "packages")); !os.IsNotExist(err) {
		t.Fatal("dry-run created a dependency link")
	}
}

func TestShareRejectsSourceOverlapAndMissingConsent(t *testing.T) {
	engine, _, cache := sharingFixture(t)
	if _, err := engine.Share(context.Background(), "daily", "A", cache, "test", true, false); err == nil {
		t.Fatal("source-overlapping mapping was accepted")
	}
	if _, err := engine.Share(context.Background(), "daily", "packages", cache, "test", false, false); err == nil {
		t.Fatal("writable sharing did not require consent")
	}
}
