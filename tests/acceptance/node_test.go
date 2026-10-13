package acceptance

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workcopy/internal/testutil"
)

// Explicit scripts are opt-in even though this fixture has no dependencies,
// downloads or lifecycle hooks. npm executes only the tiny scripts below.
func TestSelectedNodeWorkspaceBuildsAndTestsInsideLane(t *testing.T) {
	if os.Getenv("WORKCOPY_NODE_LANE_ACCEPTANCE") != "1" {
		t.Skip("set WORKCOPY_NODE_LANE_ACCEPTANCE=1 to approve the offline npm script fixture")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	npmCLI := filepath.Join(filepath.Dir(node), "node_modules", "npm", "bin", "npm-cli.js")
	if _, err := os.Stat(npmCLI); err != nil {
		npm, lookupErr := exec.LookPath("npm")
		if lookupErr != nil {
			t.Fatal("the opt-in Node fixture requires npm")
		}
		npmCLI, err = filepath.EvalSymlinks(npm)
		if err != nil || !strings.HasSuffix(npmCLI, ".js") {
			t.Fatal("could not locate npm-cli.js without executing a shell wrapper")
		}
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
		if script == "test" && !strings.Contains(string(output), "# pass 1") {
			t.Fatalf("npm test did not execute one passing test:\n%s", output)
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
