package mcpserver

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// content and resolve: an agent reads all three sides of a conflict in chunks,
// verifies the hash, and resolves it. Agent steering must stay out of the way.
func TestConflictContentCanBeReadAndResolvedOverMCP(t *testing.T) {
	session, r, _ := connect(t, Options{})
	created := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "worker"}}, Projects: []string{"A"}})
	if created.ExitCode != 0 {
		t.Fatal(created.Error)
	}
	lane := created.Report.Workspace.Path
	if err := os.WriteFile(filepath.Join(lane, "A", "a.txt"), []byte("local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	r.Write("A/a.txt", "upstream\n")
	r.Commit("upstream change to the same line")
	synced := call(t, session, "sync", Mutation{Lane: Lane{Name: "worker"}})
	if synced.ExitCode != 1 || len(synced.Report.Conflicts) != 1 {
		t.Fatalf("sync should record one conflict: %+v", synced)
	}
	conflict := synced.Report.Conflicts[0]
	for side, want := range map[string]string{"base": "base\n", "local": "local\n", "upstream": "upstream\n"} {
		out := call(t, session, "content", Content{Lane: Lane{Name: "worker"}, Token: conflict.ID, Side: side})
		if out.ExitCode != 0 || out.Report.Content == nil {
			t.Fatalf("%s: %+v", side, out.Error)
		}
		body, err := base64.StdEncoding.DecodeString(out.Report.Content.Data)
		if err != nil || string(body) != want || out.Report.Content.NextOffset != -1 || out.Report.Content.Total != int64(len(want)) {
			t.Fatalf("%s: %q %v %+v", side, body, err, out.Report.Content)
		}
	}
	chunk := call(t, session, "content", Content{Lane: Lane{Name: "worker"}, Token: conflict.ID, Side: "upstream", ByteLimit: 3})
	if chunk.Report.Content.NextOffset != 3 {
		t.Fatalf("chunked read should continue at 3: %+v", chunk.Report.Content)
	}
	if bad := call(t, session, "content", Content{Lane: Lane{Name: "worker"}, Token: "00000000000000000000000000000000", Side: "base"}); bad.ExitCode != 3 {
		t.Fatalf("an unknown token is stale: %+v", bad)
	}
	if bad := call(t, session, "content", Content{Lane: Lane{Name: "worker"}, Token: conflict.ID, Side: "sideways"}); bad.ExitCode != 2 {
		t.Fatalf("an unknown side is a usage error: %+v", bad)
	}
	resolved := call(t, session, "resolve", Resolve{Mutation: Mutation{Lane: Lane{Name: "worker"}}, Path: "A/a.txt", Token: conflict.ID, Mode: "local"})
	if resolved.ExitCode != 0 {
		t.Fatalf("resolve: %+v", resolved.Error)
	}
}

// solution: a solution filter listing only the projects present in the lane.
func TestSolutionFilterOverMCP(t *testing.T) {
	session, r, _ := connect(t, Options{})
	r.Write("B/B.csproj", `<Project/>`)
	r.Write("Repo.sln", "Microsoft Visual Studio Solution File, Format Version 12.00\nProject(\"{A}\") = \"A\", \"A\\A.csproj\", \"{B}\"\nEndProject\nProject(\"{A}\") = \"B\", \"B\\B.csproj\", \"{C}\"\nEndProject\n")
	r.Commit("solution")
	if created := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "worker"}}, Projects: []string{"A"}}); created.ExitCode != 0 {
		t.Fatal(created.Error)
	}
	preview := call(t, session, "solution", Solution{Mutation: Mutation{Lane: Lane{Name: "worker"}, DryRun: true}, Path: "Repo.sln"})
	if preview.ExitCode != 0 || preview.Report.Plan == nil {
		t.Fatalf("preview: %+v", preview.Error)
	}
	done := call(t, session, "solution", Solution{Mutation: Mutation{Lane: Lane{Name: "worker"}}, Path: "Repo.sln"})
	if done.ExitCode != 0 || len(done.Report.Plan.Files) != 1 {
		t.Fatalf("solution: %+v", done)
	}
	status := call(t, session, "status", Lane{Name: "worker"})
	body, err := os.ReadFile(filepath.Join(status.Report.Workspace.Path, filepath.FromSlash(done.Report.Plan.Files[0])))
	if err != nil || !strings.Contains(string(body), `A\\A.csproj`) || strings.Contains(string(body), "B.csproj") {
		t.Fatalf("filter must list only A: %v\n%s", err, body)
	}
	if bad := call(t, session, "solution", Solution{Mutation: Mutation{Lane: Lane{Name: "worker"}}, Path: "Missing.sln"}); bad.ExitCode == 0 {
		t.Fatal("a missing solution must fail")
	}
}

// The agent-context tools: preview, refresh, guidance, and what bootstrap and
// the guide resource tell an agent.
func TestAgentContextToolsOverMCP(t *testing.T) {
	session, r, _ := connect(t, Options{})
	r.Write(".kiro/steering/product.md", "product\n")
	r.Write(".kiro/settings/mcp.json", `{"env":{"TOKEN":"abc"}}`)
	r.Write(".newtool/rules/a.md", "an unrecognised tool\n")

	boot := call(t, session, "bootstrap", Bootstrap{})
	if boot.Context == nil || boot.Context.AgentContext == nil || boot.Context.AgentContext.Files != 1 || len(boot.Context.AgentContext.Candidates) != 1 || !strings.Contains(boot.Context.AgentContext.Guidance, "WORKCOPY_AGENT_CONTEXT_INCLUDE") {
		t.Fatalf("bootstrap should summarise agent context and name the setting: %+v", boot.Context)
	}
	preview := call(t, session, "agent_context", ContextInput{})
	if preview.ExitCode != 0 || preview.Report.AgentContext == nil || len(preview.Report.AgentContext.Included) != 1 {
		t.Fatalf("repository preview: %+v", preview)
	}
	for _, skipped := range preview.Report.AgentContext.Skipped {
		if strings.Contains(skipped.Reason, "abc") {
			t.Fatal("a refusal reason must never contain the secret")
		}
	}

	created := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "worker"}}, Projects: []string{"A"}})
	if created.ExitCode != 0 || created.Report.AgentContext == nil || strings.Join(created.Report.AgentContext.Added, ",") != ".kiro/steering/product.md" {
		t.Fatalf("create copies steering: %+v", created.Report)
	}
	laneDir := created.Report.Workspace.Path
	if _, err := os.Stat(filepath.Join(laneDir, ".kiro", "settings")); err == nil {
		t.Fatal("tool settings must not be copied")
	}
	r.Write(".kiro/steering/product.md", "product v2\n")
	laneView := call(t, session, "agent_context", ContextInput{Name: "worker"})
	if laneView.ExitCode != 0 || strings.Join(laneView.Report.AgentContext.Updated, ",") != ".kiro/steering/product.md" {
		t.Fatalf("the lane view predicts the refresh: %+v", laneView.Report.AgentContext)
	}
	if body, _ := os.ReadFile(filepath.Join(laneDir, ".kiro", "steering", "product.md")); string(body) != "product\n" {
		t.Fatal("the read-only tool must not write")
	}
	if dry := call(t, session, "refresh_context", Mutation{Lane: Lane{Name: "worker"}, DryRun: true}); dry.ExitCode != 0 {
		t.Fatal(dry.Error)
	}
	if body, _ := os.ReadFile(filepath.Join(laneDir, ".kiro", "steering", "product.md")); string(body) != "product\n" {
		t.Fatal("dry run wrote")
	}
	if done := call(t, session, "refresh_context", Mutation{Lane: Lane{Name: "worker"}}); done.ExitCode != 0 {
		t.Fatal(done.Error)
	}
	if body, _ := os.ReadFile(filepath.Join(laneDir, ".kiro", "steering", "product.md")); string(body) != "product v2\n" {
		t.Fatal("refresh did not update the copy")
	}
	if again := call(t, session, "refresh_context", Mutation{Lane: Lane{Name: "worker"}}); again.ExitCode != 4 {
		t.Fatalf("nothing left to refresh is exit 4: %+v", again)
	}

	// The guide resource and both prompts carry the agent-context rules.
	resource, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "workcopy://guide"})
	if err != nil || !strings.Contains(resource.Contents[0].Text, "workcopy_refresh_context") || !strings.Contains(resource.Contents[0].Text, "WORKCOPY_AGENT_CONTEXT_CONFIG") || !strings.Contains(resource.Contents[0].Text, "notSelected") || !strings.Contains(resource.Contents[0].Text, "Credentials, media") {
		t.Fatalf("guide resource: %v", err)
	}
}
