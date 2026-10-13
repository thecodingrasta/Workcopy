package repository

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"workcopy/internal/model"
	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

func sourceRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func TestRequiredPublicAssetsAndLocalDocumentationLinks(t *testing.T) {
	root := sourceRoot(t)
	for _, name := range []string{"README.md", "CONTRIBUTING.md", "CODE_OF_CONDUCT.md", "SECURITY.md", "SUPPORT.md", "LICENSE", "COPYRIGHT", "THIRD_PARTY_NOTICES.md", "CHANGELOG.md", "AGENTS.md", ".editorconfig", ".gitattributes", "docs/validation.md", "prebuilt/README.md", "scripts/dev.sh", "scripts/dev.ps1", ".github/workflows/ci.yml", ".github/workflows/release.yml", ".github/pull_request_template.md", "schema/workcopy.schema.json", "schema/response.schema.json"} {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || len(body) == 0 {
			t.Errorf("required asset %s: %v", name, err)
		}
	}
	links := regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "dist" || entry.Name() == ".cache" || entry.Name() == ".codex" || entry.Name() == "vendor" || entry.Name() == ".gitnexus" {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(name) != ".md" {
			return nil
		}
		body, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		for _, match := range links.FindAllSubmatch(body, -1) {
			target := strings.SplitN(string(match[1]), "#", 2)[0]
			if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if _, err = os.Stat(filepath.Join(filepath.Dir(name), filepath.FromSlash(target))); err != nil {
				t.Errorf("broken local link in %s: %s", name, target)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestPublishedConfigurationExamplesCreateRealLanes(t *testing.T) {
	root := sourceRoot(t)
	entries, err := filepath.Glob(filepath.Join(root, "examples", "*.workcopy.json"))
	if err != nil || len(entries) != 3 {
		t.Fatal("configuration examples missing", err)
	}
	for _, file := range entries {
		t.Run(filepath.Base(file), func(t *testing.T) {
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var config model.Config
			if err = model.DecodeStrict(body, &config); err != nil || config.SchemaVersion != 1 {
				t.Fatal("invalid example", err)
			}
			r := testutil.NewRepository(t)
			r.Write(".workcopy.json", string(body))
			r.Write("README.md", "Fixture\n")
			if len(config.Projects) == 0 {
				r.Write("src/App/App.csproj", "<Project/>\n")
				r.Write("src/App/Program.cs", "class Program {}\n")
			}
			for _, project := range config.Projects {
				r.Write(project.Path+"/source.txt", project.ID+"\n")
			}
			r.Commit("example fixture")
			engine, err := workspace.New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := engine.Create(context.Background(), "example", workspace.CreateOptions{}, false)
			if err != nil {
				t.Fatal(err)
			}
			if config.DefaultProfile != "" {
				selected := map[string]bool{}
				for _, id := range config.Profiles[config.DefaultProfile].Projects {
					selected[id] = true
				}
				for _, project := range config.Projects {
					_, err = os.Stat(filepath.Join(result.Workspace.Path, filepath.FromSlash(project.Path), "source.txt"))
					if selected[project.ID] && err != nil || !selected[project.ID] && !os.IsNotExist(err) {
						t.Errorf("example selection incorrect for %s: %v", project.ID, err)
					}
				}
			}
			if _, err = engine.Destroy(context.Background(), "example", false, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestWorkflowsPinRemoteActionsAndKeepReleaseManual(t *testing.T) {
	root := sourceRoot(t)
	entries, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	uses := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*([^\s#]+)`)
	pinned := regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)
	for _, name := range entries {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range uses.FindAllSubmatch(body, -1) {
			if !strings.HasPrefix(string(match[1]), "./") && !pinned.Match(match[1]) {
				t.Errorf("unpinned action: %s", match[1])
			}
		}
	}
	release, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, gate := range []string{"workflow_dispatch:", "needs: validate", "needs: [package, smoke]", "--verify-tag", "--draft", "--prerelease", "persist-credentials: false"} {
		if !strings.Contains(string(release), gate) {
			t.Errorf("release gate missing: %s", gate)
		}
	}
	ci, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, gate := range []string{"github.event_name == 'push'", "github.ref == 'refs/heads/main'", "WORKCOPY_NODE_LANE_ACCEPTANCE: '1'", "WORKCOPY_DOTNET_LANE_ACCEPTANCE: '1'", "libsynchronization.a", "--native"} {
		if !strings.Contains(string(ci), gate) {
			t.Errorf("CI gate missing: %s", gate)
		}
	}
}

func TestPublicLicencePrivacyAndDocumentationStyle(t *testing.T) {
	root := sourceRoot(t)
	license, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil || !strings.Contains(string(license), "GNU GENERAL PUBLIC LICENSE") || !strings.Contains(string(license), "Version 3, 29 June 2007") {
		t.Fatal("GNU GPL v3 text missing", err)
	}
	notice, err := os.ReadFile(filepath.Join(root, "COPYRIGHT"))
	if err != nil || !strings.Contains(string(notice), "SPDX-License-Identifier: GPL-3.0-or-later") {
		t.Fatal("GPL v3 or later project notice missing", err)
	}
	ignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"/.codex/", "/.cache/", "/dist/", "/vendor/"} {
		if !strings.Contains(string(ignore), name+"\n") {
			t.Error("private/generated scope not ignored", name)
		}
	}
	paths, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", "CONTRIBUTING.md", "SUPPORT.md", "SECURITY.md", "CHANGELOG.md", "prebuilt/README.md"} {
		paths = append(paths, filepath.Join(root, filepath.FromSlash(name)))
	}
	privatePath := regexp.MustCompile(`(?i)[A-Z]:[\\/]+Users[\\/]`)
	for _, name := range paths {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "\u2014") || privatePath.Match(body) {
			t.Error("documentation contains an em dash or private home path", name)
		}
	}
}
