package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workcopy/internal/settings"
	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

// connectAs connects a client that announces itself under clientName, to a
// server over a repository holding the untracked instruction files of several
// agent tools. values are owner settings.
func connectAs(t *testing.T, clientName string, values map[string]string) (*mcp.ClientSession, *testutil.Repository) {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write("A/A.csproj", `<Project/>`)
	r.Write("A/a.txt", "base\n")
	r.Commit("fixture")
	for name, body := range map[string]string{
		"CLAUDE.md": "claude\n", ".claude/agents/r.md": "claude agent\n", ".codex/prompts/p.md": "codex\n",
		".gemini/GEMINI.md": "gemini\n", ".kiro/steering/product.md": "kiro\n", ".opencode/agent/b.md": "opencode\n",
		".kiro/settings/mcp.json": `{"mcpServers":{"mine":{"command":"node"}}}`,
	} {
		r.Write(name, body)
	}
	e, err := workspace.New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	e.Settings, err = settings.Load(settings.Inputs{
		Environ:   func(key string) (string, bool) { value, ok := values[key]; return value, ok },
		ConfigDir: func() (string, error) { return "", errors.New("no per-user directory in tests") },
		ReadFile:  func(string) ([]byte, error) { return nil, os.ErrNotExist },
	})
	if err != nil {
		t.Fatal(err)
	}
	server := New(e, "test", Options{})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	go func() { _ = server.Run(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, r
}

// The point of the feature end to end: a real MCP session announces a client
// name, and the lane it creates holds that tool's instructions and not the
// others. The names are those the clients announce (or fragments of them).
func TestLaneIsBuiltForTheClientThatAskedOverMCP(t *testing.T) {
	cases := []struct {
		client string
		want   []string
		never  []string
	}{
		{"claude-code", []string{"CLAUDE.md", ".claude/agents/r.md"}, []string{".codex/prompts/p.md", ".gemini/GEMINI.md", ".kiro/steering/product.md", ".opencode/agent/b.md"}},
		{"codex-mcp-client", []string{".codex/prompts/p.md"}, []string{"CLAUDE.md", ".gemini/GEMINI.md", ".kiro/steering/product.md"}},
		{"gemini-cli-mcp-client", []string{".gemini/GEMINI.md"}, []string{"CLAUDE.md", ".codex/prompts/p.md", ".kiro/steering/product.md"}},
		{"Kiro CLI", []string{".kiro/steering/product.md"}, []string{"CLAUDE.md", ".codex/prompts/p.md", ".gemini/GEMINI.md"}},
		{"opencode", []string{".opencode/agent/b.md"}, []string{"CLAUDE.md", ".kiro/steering/product.md"}},
		{"an-unknown-client", []string{"CLAUDE.md", ".codex/prompts/p.md", ".gemini/GEMINI.md", ".kiro/steering/product.md", ".opencode/agent/b.md"}, nil},
	}
	for _, c := range cases {
		t.Run(c.client, func(t *testing.T) {
			session, _ := connectAs(t, c.client, nil)
			boot := call(t, session, "bootstrap", Bootstrap{})
			summary := boot.Context.AgentContext
			if summary == nil || summary.Client != c.client || summary.AgentsSource == "" {
				t.Fatalf("bootstrap must tell the agent what it will be served and why: %+v", summary)
			}
			created := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "worker"}}, Projects: []string{"A"}})
			if created.ExitCode != 0 {
				t.Fatal(created.Error)
			}
			lane := created.Report.Workspace.Path
			for _, name := range c.want {
				if !fileExists(filepath.Join(lane, filepath.FromSlash(name))) {
					t.Errorf("%s missing from the lane built for %s", name, c.client)
				}
			}
			for _, name := range c.never {
				if fileExists(filepath.Join(lane, filepath.FromSlash(name))) {
					t.Errorf("%s belongs to another tool and was copied for %s", name, c.client)
				}
			}
			if fileExists(filepath.Join(lane, ".kiro", "settings", "mcp.json")) {
				t.Error("tool configuration is never copied without an owner opt-in")
			}
		})
	}
}

// A caller can name the agent itself; an owner setting and an unknown name are
// handled; an agent cannot opt configuration in; and switching works through
// the refresh tool.
func TestCallerOwnerAndRefreshControlsOverMCP(t *testing.T) {
	session, _ := connectAs(t, "claude-code", map[string]string{settings.Agents: "auto;kiro"})
	boot := call(t, session, "bootstrap", Bootstrap{})
	if got := strings.Join(boot.Context.AgentContext.Agents, ","); got != "claude,kiro" {
		t.Fatalf("auto plus an owner extra: %s", got)
	}

	if bad := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "typo"}}, Projects: []string{"A"}, Agents: []string{"claud"}}); bad.ExitCode != 2 || bad.Error == nil || !strings.Contains(bad.Error.Message, "claude") {
		t.Fatalf("a misspelt tool name is a usage error that lists the valid names: %+v", bad)
	}
	if call(t, session, "list", Empty{}).Report == nil {
		t.Fatal("list")
	}

	created := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "worker"}}, Projects: []string{"A"}, Agents: []string{"codex"}})
	if created.ExitCode != 0 || strings.Join(created.Report.AgentContext.Agents, ",") != "codex" || !strings.Contains(created.Report.AgentContext.AgentsSource, "requested") {
		t.Fatalf("the caller's choice beats auto and the owner extra: %+v", created.Report.AgentContext)
	}
	lane := created.Report.Workspace.Path
	if !fileExists(filepath.Join(lane, ".codex", "prompts", "p.md")) || fileExists(filepath.Join(lane, "CLAUDE.md")) {
		t.Fatal("lane contents do not match the request")
	}

	preview := call(t, session, "agent_context", ContextInput{Agents: []string{"gemini"}})
	if preview.ExitCode != 0 || strings.Join(preview.Report.AgentContext.Agents, ",") != "gemini" {
		t.Fatalf("agent_context previews a chosen tool: %+v", preview)
	}
	switched := call(t, session, "refresh_context", RefreshInput{Mutation: Mutation{Lane: Lane{Name: "worker"}}, Agents: []string{"gemini"}})
	if switched.ExitCode != 0 || !fileExists(filepath.Join(lane, ".gemini", "GEMINI.md")) || fileExists(filepath.Join(lane, ".codex", "prompts", "p.md")) {
		t.Fatalf("refresh_context switches the lane's tool: %+v", switched)
	}
	kept := call(t, session, "refresh_context", Mutation{Lane: Lane{Name: "worker"}})
	if kept.ExitCode != 4 {
		t.Fatalf("a refresh with no argument keeps the recorded tool and has nothing to do: %+v", kept)
	}
}

// No tool argument lets a caller copy configuration: the opt-in lists belong to
// the owner's settings. The tool schemas must not offer one.
func TestAgentCannotOptConfigurationInOverMCP(t *testing.T) {
	session, _ := connectAs(t, "claude-code", nil)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		schema, _ := tool.InputSchema.(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		for name := range properties {
			lower := strings.ToLower(name)
			if strings.Contains(lower, "config") && tool.Name != "workcopy_create" && tool.Name != "workcopy_discover" || strings.Contains(lower, "secret") || strings.Contains(lower, "include") && strings.Contains(tool.Name, "context") {
				t.Errorf("%s offers %q, which would let an agent widen what is copied", tool.Name, name)
			}
		}
	}
	// An argument the schema does not define is rejected, not ignored.
	if _, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "workcopy_create", Arguments: map[string]any{"name": "x", "projects": []string{"A"}, "agentContextConfig": []string{".kiro/settings/mcp.json"}}}); err == nil {
		out := call(t, session, "agent_context", ContextInput{})
		for _, f := range out.Report.AgentContext.Included {
			if strings.Contains(f.Path, "settings") {
				t.Fatalf("an unknown argument opened configuration: %s", f.Path)
			}
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
