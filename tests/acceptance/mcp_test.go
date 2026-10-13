package acceptance

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workcopy/internal/mcpserver"
	"workcopy/internal/model"
	"workcopy/internal/testutil"
)

func startMCP(t *testing.T, repo, root string) *mcp.ClientSession {
	t.Helper()
	return startMCPArgs(t, "--repo", repo, "--root", root, "--max-lanes", "5")
}

func startMCPArgs(t *testing.T, args ...string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	cmd := exec.Command(executable, append([]string{"mcp"}, args...)...)
	cmd.Stderr = os.Stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "workcopy-acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestMCPMultipleRepositoriesAndDirectoryOnboarding(t *testing.T) {
	parent := t.TempDir()
	session := startMCPArgs(t, "--root", parent, "--max-lanes", "5")
	mcpCall(t, session, "doctor", map[string]any{}, 0)
	mcpCall(t, session, "bootstrap", map[string]any{}, 2)
	var workers sync.WaitGroup
	for i := 0; i < 3; i++ {
		r := testutil.NewRepository(t)
		r.Write("go.mod", "module fixture\n\ngo 1.25.0\n")
		r.Write("value.go", "package fixture\nfunc Value() int { return 1 }\n")
		r.Write("value_test.go", "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 2 { t.Fatal(Value()) } }\n")
		r.Commit("offline repository")
		head, index := r.Git("rev-parse", "HEAD"), r.Git("ls-files", "--stage")
		boot := mcpCall(t, session, "bootstrap", map[string]any{"directory": r.Path}, 0)
		root := boot.Scope.Root
		if i == 2 {
			root = filepath.Join(r.Parent, "override")
		}
		args := map[string]any{"repo": r.Path, "root": root, "name": "worker", "projects": []string{"go.mod"}, "dryRun": true}
		mcpCall(t, session, "create", args, 0)
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("preview persisted a root", err)
		}
		args["dryRun"] = false
		created := mcpCall(t, session, "create", args, 0)
		lane := created.Report.Workspace.Path
		laneInfo := mcpCall(t, session, "bootstrap", map[string]any{"directory": lane}, 0)
		if laneInfo.Context.Lane.Name != "worker" || laneInfo.Scope.Root != root || laneInfo.Scope.Repo != r.Path {
			t.Fatal("lane entry lost scope", laneInfo)
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := os.WriteFile(filepath.Join(lane, "value.go"), []byte("package fixture\nfunc Value() int { return 2 }\n"), 0644); err != nil {
				t.Error(err)
				return
			}
			validationCtx, cancelValidation := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancelValidation()
			cmd := exec.CommandContext(validationCtx, "go", "test", "./...")
			cmd.Dir = lane
			cmd.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0")
			if body, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("lane validation: %v %s", err, body)
				return
			}
			scope := map[string]any{"repo": r.Path, "root": root, "name": "worker", "dryRun": false}
			mcpCall(t, session, "sync", scope, 4)
			review := mcpCall(t, session, "review", scope, 0)
			if review.Report.Review == nil || review.Report.Review.ID == "" {
				t.Error("review handoff missing")
			}
			body, err := os.ReadFile(filepath.Join(r.Path, "value.go"))
			if err != nil || string(body) != "package fixture\nfunc Value() int { return 1 }\n" || r.Git("rev-parse", "HEAD") != head || r.Git("ls-files", "--stage") != index {
				t.Error("source checkout mutated", err)
			}
		}()
	}
	workers.Wait()
}
func mcpCall(t *testing.T, session *mcp.ClientSession, tool string, args any, want int) mcpserver.Output {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "workcopy_" + tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	body, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out mcpserver.Output
	if err = json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.ExitCode != want || out.SchemaVersion != 1 || result.IsError != (want != 0 && want != 4) {
		t.Fatalf("%s wanted %d: %s", tool, want, body)
	}
	return out
}

func TestMCPThreeWorkersValidateAndHandOffReviews(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("AGENTS.md", "Keep consumer instructions.\n")
	for i := 1; i <= 25; i++ {
		p := fmt.Sprintf("projects/p%02d", i)
		r.Write(p+"/go.mod", fmt.Sprintf("module fixture/p%02d\n\ngo 1.23.0\n", i))
		r.Write(p+"/value.go", "package fixture\nfunc Value() int { return 1 }\n")
		r.Write(p+"/value_test.go", "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() < 1 { t.Fatal(\"invalid\") } }\n"+strings.Repeat("// Small offline fixture source.\n", 256))
	}
	r.Commit("25 offline projects")
	head := r.Git("rev-parse", "HEAD")
	symbolic := r.Git("symbolic-ref", "HEAD")
	r.Write("dirty.txt", "human untracked\n")
	r.Write("AGENTS.md", "Human unstaged instructions\n")
	index := r.Git("ls-files", "--stage")
	dirty := r.Git("status", "--porcelain=v1")
	root := filepath.Join(r.Parent, "lanes")
	session := startMCP(t, r.Path, root)
	boot := mcpCall(t, session, "bootstrap", map[string]any{"limit": 10}, 0)
	if boot.Pages["projects"].Total != 25 || boot.Pages["projects"].NextOffset != 10 {
		t.Fatal("catalog pagination missing", boot.Pages)
	}
	if session.InitializeResult().ProtocolVersion != "2026-07-28" {
		t.Fatal("current protocol not negotiated", session.InitializeResult().ProtocolVersion)
	}
	paths := map[string]string{}
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("worker-%d", i)
		project := fmt.Sprintf("projects/p%02d", i)
		mcpCall(t, session, "create", map[string]any{"name": name, "projects": []string{project}, "dryRun": true}, 0)
		out := mcpCall(t, session, "create", map[string]any{"name": name, "projects": []string{project}, "dryRun": false}, 0)
		paths[name] = out.Report.Workspace.Path
		if _, err := os.Stat(filepath.Join(paths[name], ".git")); !os.IsNotExist(err) {
			t.Fatal("lane has Git metadata")
		}
		if _, err := os.Stat(filepath.Join(paths[name], "projects", "p25")); !os.IsNotExist(err) {
			t.Fatal("excluded project copied")
		}
		c := mcpCall(t, session, "lane_context", map[string]any{"name": name}, 0)
		if c.Context.Lane.Path != paths[name] || len(c.Context.Validation) != 1 {
			t.Fatal("worker bootstrap missing")
		}
		if i == 1 || i == 3 {
			logLaneStorage(t, root, i)
		}
	}
	for i := 4; i <= 5; i++ {
		name := fmt.Sprintf("idle-%d", i)
		mcpCall(t, session, "create", map[string]any{"name": name, "projects": []string{fmt.Sprintf("projects/p%02d", i)}, "dryRun": false}, 0)
	}
	logLaneStorage(t, root, 5)
	var workers sync.WaitGroup
	for i := 1; i <= 3; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			name := fmt.Sprintf("worker-%d", i)
			p := filepath.Join(paths[name], "projects", fmt.Sprintf("p%02d", i))
			if err := os.WriteFile(filepath.Join(p, "value.go"), []byte(fmt.Sprintf("package fixture\nfunc Value() int { return %d }\n", i+1)), 0644); err != nil {
				t.Error(err)
				return
			}
			validationCtx, cancelValidation := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancelValidation()
			cmd := exec.CommandContext(validationCtx, "go", "test", "./...")
			cmd.Dir = p
			cmd.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0")
			if body, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("isolated offline validation %d: %v %s", i, err, body)
			}
		}(i)
	}
	workers.Wait()
	// Deliberate overlap between lane one and upstream; the other workers
	// remain independent and are not mutated by its conflict or resolution.
	r.Write("projects/p01/value.go", "package fixture\nfunc Value() int { return 20 }\n")
	r.Git("add", "projects/p01/value.go")
	r.Git("commit", "-m", "human simulated upstream change")
	expectedHead := r.Git("rev-parse", "HEAD")
	expectedIndex := r.Git("ls-files", "--stage")
	out := mcpCall(t, session, "sync", map[string]any{"name": "worker-1", "dryRun": false}, 1)
	if len(out.Report.Conflicts) != 1 {
		t.Fatal("conflict hidden")
	}
	conflict := out.Report.Conflicts[0]
	content := mcpCall(t, session, "content", map[string]any{"name": "worker-1", "token": conflict.ID, "side": "local", "byteLimit": 8}, 0)
	var assembled []byte
	for {
		chunk := content.Report.Content
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil {
			t.Fatal(err)
		}
		assembled = append(assembled, data...)
		if chunk.NextOffset == -1 {
			if model.Hash(assembled) != chunk.SHA256 {
				t.Fatal("content digest mismatch")
			}
			break
		}
		content = mcpCall(t, session, "content", map[string]any{"name": "worker-1", "token": conflict.ID, "side": "local", "byteLimit": 8, "byteOffset": chunk.NextOffset}, 0)
	}
	mcpCall(t, session, "resolve", map[string]any{"name": "worker-1", "path": conflict.Path, "token": conflict.ID, "mode": "local", "dryRun": false}, 0)
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("worker-%d", i)
		status := mcpCall(t, session, "status", map[string]any{"name": name}, 0)
		if len(status.Report.Changes) != 1 {
			t.Fatal("lost contribution", status.Report)
		}
		review := mcpCall(t, session, "review", map[string]any{"name": name, "dryRun": false}, 0)
		if review.Report.Review == nil || review.Report.Review.State != "prepared" {
			t.Fatal("review missing")
		}
		if len(review.Report.Review.Files) != 0 {
			t.Fatal("unbounded review inventory exposed")
		}
		cmd := exec.Command("git", "-C", review.Report.Review.Path, "diff", "--cached", "--quiet")
		if err := cmd.Run(); err != nil {
			t.Fatal("review was staged")
		}
		mcpCall(t, session, "destroy", map[string]any{"name": name, "dryRun": true}, 3)
	}
	if r.Git("symbolic-ref", "HEAD") != symbolic || r.Git("status", "--porcelain=v1") != dirty {
		t.Fatal("original human checkout edits changed")
	}
	if r.Git("rev-parse", "HEAD") != expectedHead || r.Git("ls-files", "--stage") != expectedIndex {
		t.Fatal("MCP changed original HEAD or index")
	}
	t.Logf("three retained reviews: allocated file bytes %d; complete scenario allocation %d", measureAllocated(t, filepath.Join(root, ".workcopy-reviews")), measureAllocated(t, r.Parent))
	if allocated := measureAllocated(t, r.Parent); allocated > 32<<20 {
		t.Fatalf("scenario exceeds 32 MiB allocated fixture budget: %d", allocated)
	}
	// Upstream advanced only through the explicit fixture commit above.
	if r.Git("rev-parse", "HEAD") == head || r.Git("ls-files", "--stage") == index {
		t.Fatal("fixture upstream commit not established")
	}
	for i := 2; i <= 3; i++ {
		body, err := os.ReadFile(filepath.Join(paths[fmt.Sprintf("worker-%d", i)], "projects", fmt.Sprintf("p%02d", i), "value.go"))
		if err != nil || string(body) != fmt.Sprintf("package fixture\nfunc Value() int { return %d }\n", i+1) {
			t.Fatal("cross-lane source mutation")
		}
	}
	// Only the fixture's human integration advances main; a prepared review
	// itself never cleans the contribution or authorizes destruction.
	for i := 1; i <= 3; i++ {
		file := fmt.Sprintf("projects/p%02d/value.go", i)
		r.Write(file, fmt.Sprintf("package fixture\nfunc Value() int { return %d }\n", i+1))
		r.Git("add", file)
	}
	r.Git("commit", "-m", "human integration of reviewed contributions")
	expectedHead = r.Git("rev-parse", "HEAD")
	expectedIndex = r.Git("ls-files", "--stage")
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("worker-%d", i)
		mcpCall(t, session, "sync", map[string]any{"name": name, "dryRun": false}, 0)
		status := mcpCall(t, session, "status", map[string]any{"name": name}, 0)
		if len(status.Report.Changes) != 0 {
			t.Fatal("integrated contribution remained dirty")
		}
		mcpCall(t, session, "destroy", map[string]any{"name": name, "dryRun": true}, 0)
		mcpCall(t, session, "destroy", map[string]any{"name": name, "dryRun": false}, 0)
	}
	if r.Git("rev-parse", "HEAD") != expectedHead || r.Git("ls-files", "--stage") != expectedIndex || r.Git("status", "--porcelain=v1") != dirty {
		t.Fatal("sync/cleanup changed human checkout")
	}
}

func measureAllocated(t *testing.T, root string) uint64 {
	t.Helper()
	var total uint64
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			n, err := allocatedFileBytes(name)
			if err != nil {
				return err
			}
			total += n
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return total
}
func logLaneStorage(t *testing.T, root string, count int) {
	t.Helper()
	var source, metadata uint64
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		n, err := allocatedFileBytes(name)
		if err != nil {
			return err
		}
		if strings.Contains(relative, "/.workcopy/") || strings.HasPrefix(relative, ".workcopy-") {
			metadata += n
		} else {
			source += n
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	copies := filepath.Join(filepath.Dir(root), fmt.Sprintf("ordinary-copies-%d", count))
	if err = os.Mkdir(copies, 0700); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".workcopy" || strings.HasPrefix(entry.Name(), ".workcopy-")) {
			return filepath.SkipDir
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		target := filepath.Join(copies, relative)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		body, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
	copyAllocated := measureAllocated(t, copies)
	if copyAllocated != source {
		t.Fatalf("equivalent ordinary copy differs: %d vs %d", copyAllocated, source)
	}
	t.Logf("%d lanes: allocated source=%d metadata=%d equivalent ordinary-copy source=%d package stores=0 lane build outputs=0 (Go compiler cache separately configured)", count, source, metadata, copyAllocated)
}

func TestMCPLegacyClientAndProtocolOnlyStdout(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("a.txt", "base\n")
	r.Commit("base")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "mcp", "--repo", r.Path, "--root", filepath.Join(r.Parent, "lanes"))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	fmt.Fprintln(stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"legacy-test","version":"1"}}}`)
	if !scanner.Scan() {
		t.Fatal("no initialize response")
	}
	var response struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
	}
	if err = json.Unmarshal(scanner.Bytes(), &response); err != nil || response.Result.ProtocolVersion != "2025-11-25" {
		t.Fatalf("legacy response: %s %v", scanner.Text(), err)
	}
	fmt.Fprintln(stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	fmt.Fprintln(stdin, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if !scanner.Scan() || !json.Valid(scanner.Bytes()) {
		t.Fatal("stdout contained non-protocol data")
	}
}
