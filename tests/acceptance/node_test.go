package acceptance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"workcopy/internal/testutil"
)

var (
	nodeTestsOne = regexp.MustCompile(`(?m)(?:^|\s)tests\s+1(?:\s|$)`)
	nodePassOne  = regexp.MustCompile(`(?m)(?:^|\s)pass\s+1(?:\s|$)`)
	nodeFailures = regexp.MustCompile(`(?m)(?:^|\s)fail\s+[1-9]`)
)

func findNodeAndNPMCLI() (string, string, error) {
	pathEntries := filepath.SplitList(os.Getenv("PATH"))
	for _, directory := range pathEntries {
		for _, executable := range []string{"node.exe", "node"} {
			node := filepath.Join(directory, executable)
			if !regularFile(node) {
				continue
			}
			for _, npmCLI := range []string{
				filepath.Join(directory, "node_modules", "npm", "bin", "npm-cli.js"),
				filepath.Join(directory, "..", "lib", "node_modules", "npm", "bin", "npm-cli.js"),
				filepath.Join(directory, "..", "share", "nodejs", "npm", "bin", "npm-cli.js"),
			} {
				if regularFile(npmCLI) {
					return node, npmCLI, nil
				}
			}
		}
	}
	for _, commandName := range []string{"npm.cmd", "npm", "npm.ps1"} {
		npm, err := exec.LookPath(commandName)
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(npm)
		if err == nil && strings.HasSuffix(strings.ToLower(filepath.ToSlash(resolved)), "/npm/bin/npm-cli.js") {
			node, nodeErr := exec.LookPath("node")
			if nodeErr == nil {
				return node, resolved, nil
			}
		}
	}
	return "", "", fmt.Errorf("could not locate a native node executable paired with npm-cli.js on PATH")
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func nodeTestPassed(output string) bool {
	return nodeTestsOne.MatchString(output) && nodePassOne.MatchString(output) && !nodeFailures.MatchString(output)
}

// Explicit scripts are opt-in even though this fixture has no dependencies,
// downloads or lifecycle hooks. npm executes only the tiny scripts below.
func TestSelectedNodeWorkspaceBuildsAndTestsInsideLane(t *testing.T) {
	if os.Getenv("WORKCOPY_NODE_LANE_ACCEPTANCE") != "1" {
		t.Skip("set WORKCOPY_NODE_LANE_ACCEPTANCE=1 to approve the offline npm script fixture")
	}
	node, npmCLI, err := findNodeAndNPMCLI()
	if err != nil {
		t.Fatal(err)
	}
	r := testutil.NewRepository(t)
	r.Write(".gitignore", ".lane-build/\n")
	r.Write("package.json", `{"private":true,"packageManager":"npm@10.3.0","workspaces":["packages/*"]}`)
	r.Write("package-lock.json", `{"name":"offline-fixture","lockfileVersion":3,"requires":true,"packages":{}}`)
	r.Write(".workcopy.json", `{"schemaVersion":1,"projects":[{"id":"app","path":"packages/app","dependencies":["core"]},{"id":"core","path":"packages/core"},{"id":"excluded","path":"packages/excluded"}],"profiles":{"daily":{"projects":["app"],"include":["package.json","package-lock.json",".gitignore"],"dependencyClosure":"include"}}}`)
	r.Write("packages/core/package.json", `{"name":"@fixture/core","version":"1.0.0","type":"module"}`)
	r.Write("packages/core/value.mjs", "export const value = 1;\n")
	r.Write("packages/app/package.json", `{"name":"@fixture/app","version":"1.0.0","type":"module","scripts":{"build":"node build.mjs","test":"node --test test.mjs"}}`)
	r.Write("packages/app/build.mjs", "import { mkdir, writeFile } from 'node:fs/promises';\nimport { value } from '../core/value.mjs';\nconst output = new URL('../../.lane-build/', import.meta.url);\nawait mkdir(output, { recursive: true });\nawait writeFile(new URL('result.txt', output), String(value));\n")
	r.Write("packages/app/test.mjs", "import test from 'node:test';\nimport assert from 'node:assert/strict';\nimport { readFile } from 'node:fs/promises';\nimport { value } from '../core/value.mjs';\ntest('selected workspace contribution builds locally', async () => {\n  assert.equal(value, 2);\n  assert.equal(await readFile(new URL('../../.lane-build/result.txt', import.meta.url), 'utf8'), '2');\n});\n")
	r.Write("packages/excluded/package.json", `{"name":"excluded","version":"1.0.0"}`)
	r.Commit("offline Node workspace")
	head, index, symbolic := r.Git("rev-parse", "HEAD"), r.Git("ls-files", "--stage"), r.Git("symbolic-ref", "HEAD")
	r.Write("human.txt", "preserve human edits\n")
	dirty := r.Git("status", "--porcelain=v1")
	lane := invoke(t, r.Path, 0, "create", "node-app", "--root", filepath.Join(r.Parent, "lanes"), "--profile", "daily").Workspace.Path
	for _, absent := range []string{".git", "packages/excluded", "node_modules"} {
		if _, err := os.Lstat(filepath.Join(lane, absent)); !os.IsNotExist(err) {
			t.Fatalf("unexpected selected input %s: %v", absent, err)
		}
	}
	if err := os.WriteFile(filepath.Join(lane, "packages/core/value.mjs"), []byte("export const value = 2;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"npm-user.conf", "npm-global.conf"} {
		if err := os.WriteFile(filepath.Join(r.Parent, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, script := range []string{"build", "test"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, node, npmCLI, "--offline", "--ignore-scripts", "run", script)
		cmd.Dir = filepath.Join(lane, "packages/app")
		for _, item := range os.Environ() {
			key := strings.ToUpper(strings.SplitN(item, "=", 2)[0])
			if !strings.HasPrefix(key, "NPM_CONFIG_") && key != "NODE_OPTIONS" && key != "NODE_PATH" {
				cmd.Env = append(cmd.Env, item)
			}
		}
		cmd.Env = append(cmd.Env, "NPM_CONFIG_CACHE="+filepath.Join(r.Parent, "npm-cache"), "NPM_CONFIG_USERCONFIG="+filepath.Join(r.Parent, "npm-user.conf"), "NPM_CONFIG_GLOBALCONFIG="+filepath.Join(r.Parent, "npm-global.conf"), "NPM_CONFIG_UPDATE_NOTIFIER=false", "NPM_CONFIG_AUDIT=false", "NPM_CONFIG_FUND=false", "NPM_CONFIG_LOGS_MAX=1")
		output, runErr := cmd.CombinedOutput()
		cancel()
		if runErr != nil {
			t.Fatalf("offline npm run %s: %v\n%s", script, runErr, output)
		}
		if script == "test" && !nodeTestPassed(string(output)) {
			t.Fatalf("npm test did not report one passing test:\n%s", output)
		}
		t.Logf("npm run %s exit 0", script)
	}
	status := invoke(t, lane, 0, "status")
	if len(status.Changes) != 1 || status.Changes[0].Path != "packages/core/value.mjs" {
		t.Fatalf("build outputs affected contribution: %+v", status.Changes)
	}
	if r.Git("rev-parse", "HEAD") != head || r.Git("symbolic-ref", "HEAD") != symbolic || r.Git("ls-files", "--stage") != index || r.Git("status", "--porcelain=v1") != dirty {
		t.Fatal("offline Node validation changed the source checkout")
	}
	if _, err := os.Stat(filepath.Join(r.Path, ".lane-build")); !os.IsNotExist(err) {
		t.Fatal("build output escaped the lane")
	}
	allocated := measureAllocated(t, r.Parent)
	t.Logf("offline Node scenario allocated file bytes: %d", allocated)
	if allocated > 32<<20 {
		t.Fatalf("Node fixture exceeds 32 MiB budget: %d", allocated)
	}
}

func TestNodeTestPassedAcceptsSupportedSummaryFormats(t *testing.T) {
	for _, output := range []string{
		"# tests 1\n# pass 1\n# fail 0\n",
		"ℹ tests 1\nℹ pass 1\nℹ fail 0\n",
	} {
		if !nodeTestPassed(output) {
			t.Fatalf("accepted Node summary was rejected: %q", output)
		}
	}
	if nodeTestPassed("ℹ tests 1\nℹ pass 0\nℹ fail 1\n") {
		t.Fatal("failed Node summary was accepted")
	}
}
