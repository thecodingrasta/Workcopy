package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

func connect(t *testing.T, opts Options) (*mcp.ClientSession, *testutil.Repository, *Server) {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write("A/A.csproj", `<Project/>`)
	r.Write("A/a.txt", "base\n")
	r.Write("eng/import.props", `<Project/>`)
	r.Write("AGENTS.md", "Consumer policy\n")
	r.Commit("fixture")
	e, err := workspace.New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(e, "test", opts)
	return connectServer(t, s), r, s
}

func connectServer(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	go func() { _ = s.Run(ctx, serverTransport) }()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
func call(t *testing.T, session *mcp.ClientSession, name string, args any) Output {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "workcopy_" + name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out Output
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.SchemaVersion != 1 || r.IsError != (out.ExitCode != 0 && out.ExitCode != 4) {
		t.Fatalf("bad MCP error contract: %s", data)
	}
	return out
}

type discardCloser struct{ io.Writer }

func (discardCloser) Close() error { return nil }

func TestCommandAcceptsOwnerConfiguration(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("A/source.txt", "source\n")
	r.Commit("explicit configuration fixture")
	config := filepath.Join(r.Parent, "owner-config.json")
	if err := os.WriteFile(config, []byte(`{"schemaVersion":1,"discovery":{"adapters":["generic"]},"projects":[{"id":"app","path":"A"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	code := Command(context.Background(), []string{"--repo", r.Path, "--root", filepath.Join(r.Parent, "lanes"), "--config", config}, "test", io.NopCloser(strings.NewReader("")), discardCloser{io.Discard}, &stderr)
	if code != 0 {
		t.Fatalf("owner startup configuration rejected: exit %d: %s", code, stderr.String())
	}
	stderr.Reset()
	code = Command(context.Background(), []string{"--repo", r.Path, "--config", filepath.Join(r.Parent, "missing.json")}, "test", io.NopCloser(strings.NewReader("")), discardCloser{io.Discard}, &stderr)
	if code != 3 || !strings.Contains(stderr.String(), "MCP owner configuration") {
		t.Fatalf("missing owner configuration accepted: exit %d: %s", code, stderr.String())
	}
}

func TestOwnerConfigurationAppliesToBootstrapDiscoveryAndCreation(t *testing.T) {
	config := filepath.Join(t.TempDir(), "owner-config.json")
	if err := os.WriteFile(config, []byte(`{"schemaVersion":1,"discovery":{"adapters":["generic"]},"projects":[{"id":"app","path":"A"}],"profiles":{"daily":{"projects":["app"],"include":["eng/import.props"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	session, r, _ := connect(t, Options{ConfigPath: config})
	r.Write("unrelated/Broken.csproj", `<Project><ItemGroup><ProjectReference Include="../missing/Missing.csproj" /></ItemGroup></Project>`)
	r.Commit("unrelated missing build input")
	for _, tool := range []string{"bootstrap", "discover"} {
		out := call(t, session, tool, Empty{})
		if out.ExitCode != 0 || out.Report.Catalog == nil || len(out.Report.Catalog.Projects) != 1 || out.Report.Catalog.Projects[0].ID != "app" {
			t.Fatalf("%s did not use owner's explicit catalog: %+v", tool, out)
		}
	}
	if err := os.WriteFile(config, []byte("changed after server startup"), 0600); err != nil {
		t.Fatal(err)
	}
	out := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "worker"}}, Profile: "daily"})
	if out.ExitCode != 0 {
		t.Fatalf("creation did not use owner's profile: exit=%d error=%+v report=%+v", out.ExitCode, out.Error, out.Report)
	}
	if _, err := os.Stat(filepath.Join(out.Report.Workspace.Path, "eng/import.props")); err != nil {
		t.Fatal("configured build input absent", err)
	}
	context := call(t, session, "lane_context", Lane{Name: "worker"})
	if context.ExitCode != 0 || len(context.Context.Selection.Projects) != 1 || context.Context.Selection.Projects[0] != "app" {
		t.Fatalf("saved lane configuration was not retained: %+v", context)
	}
}
func TestBootstrapSelectionPaginationAndLaneContext(t *testing.T) {
	session, r, s := connect(t, Options{MaxLanes: 2, ReserveBytes: 2 << 30})
	out := call(t, session, "bootstrap", Empty{Page: Page{Limit: 1}})
	if out.ExitCode != 0 || out.Context.Repository != r.Path || len(out.Context.BuildInputs) != 1 || out.Pages["buildInputs"].NextOffset != 1 {
		t.Fatalf("bootstrap: %+v", out)
	}
	if _, err := os.Stat(s.engine.WorkRoot); !os.IsNotExist(err) {
		t.Fatal("bootstrap persisted a work root")
	}
	resources, err := session.ListResources(context.Background(), nil)
	if err != nil || len(resources.Resources) != 1 {
		t.Fatalf("resources: %v %v", resources, err)
	}
	guide, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "workcopy://guide"})
	if err != nil || !strings.Contains(guide.Contents[0].Text, "git checkout/reset/commit/push") {
		t.Fatalf("guide: %v %v", guide, err)
	}
	prompt, err := session.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "workcopy_controller"})
	if err != nil || len(prompt.Messages) != 1 {
		t.Fatalf("prompt: %v", err)
	}
	if out = call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "one"}}}); out.ExitCode != 2 {
		t.Fatal("implicit all selection permitted", out)
	}
	if out = call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "one"}, DryRun: true}, Projects: []string{"A"}}); out.ExitCode != 0 {
		t.Fatalf("preview: %+v", out)
	}
	if _, err = os.Stat(s.engine.WorkRoot); !os.IsNotExist(err) {
		t.Fatal("preview persisted a work root")
	}
	if out = call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "one"}}, Projects: []string{"A"}}); out.ExitCode != 0 {
		t.Fatalf("create: %+v", out)
	}
	out = call(t, session, "lane_context", Lane{Name: "one"})
	if out.ExitCode != 0 || out.Context.Lane.Name != "one" || len(out.Context.MissingInputs) != 1 {
		t.Fatalf("lane context: %+v", out)
	}
	if body, err := os.ReadFile(filepath.Join(out.Context.Lane.Path, "AGENTS.md")); err != nil || string(body) != "Consumer policy\n" {
		t.Fatal("consumer instructions changed")
	}
	if out = call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "two"}}, Projects: []string{"A"}}); out.ExitCode != 0 {
		t.Fatal(out)
	}
	if out = call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "three"}}, Projects: []string{"A"}}); out.ExitCode != 3 || out.Error.Code != "lane-limit" {
		t.Fatal("count admission missing", out)
	}
}
func TestAdmissionAndNegativeArgumentsHaveNoMutation(t *testing.T) {
	session, _, s := connect(t, Options{ReserveBytes: ^uint64(0)})
	out := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "one"}}, Projects: []string{"A"}})
	if out.ExitCode != 3 || out.Error.Code != "disk-budget" {
		t.Fatal(out)
	}
	if _, err := os.Stat(s.engine.WorkRoot); !os.IsNotExist(err) {
		t.Fatal("blocked create persisted state")
	}
	out = call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "bad", Page: Page{Offset: -1}}}, Projects: []string{"A"}})
	if out.ExitCode != 2 {
		t.Fatal(out)
	}
	out = call(t, session, "status", Lane{Name: "../outside"})
	if out.ExitCode != 2 {
		t.Fatal(out)
	}
}
func TestToolSurfaceHasNoBypassAndRejectsUnknownArguments(t *testing.T) {
	session, _, _ := connect(t, Options{})
	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 17 {
		t.Fatalf("tools: %d", len(list.Tools))
	}
	for _, tool := range list.Tools {
		if tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil {
			t.Fatalf("missing contract: %s", tool.Name)
		}
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "workcopy_destroy", Arguments: map[string]any{"name": "one", "dryRun": false, "force": true}})
	if err == nil && (result == nil || !result.IsError) {
		t.Fatal("unknown force parameter accepted")
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "workcopy_create", Arguments: map[string]any{"name": "one", "dryRun": false, "projects": []string{"A"}, "config": "other"}})
	if err == nil && (result == nil || !result.IsError) {
		t.Fatal("arbitrary configuration path accepted")
	}
}

func TestServerInstructionsStayCompactAndActionable(t *testing.T) {
	if len(Instructions) > 1024 {
		t.Fatalf("server instructions are %d bytes; expected at most 1024", len(Instructions))
	}
	for _, required := range []string{
		"workcopy_bootstrap",
		"workcopy://guide",
		"scope.repo",
		"scope.root",
		"dry-run",
		"not a sandbox",
		"not integration",
		"git checkout/reset/commit/push",
	} {
		if !strings.Contains(Instructions, required) {
			t.Errorf("server instructions omit %q", required)
		}
	}
	session := connectServer(t, New(nil, "test", Options{}))
	initialized := session.InitializeResult()
	if initialized == nil || initialized.Instructions != Instructions {
		t.Fatal("MCP initialize did not publish the compact server instructions")
	}
	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if len(tool.Description) > 400 {
			t.Errorf("tool %s description is %d bytes; keep detailed guidance in the guide resource", tool.Name, len(tool.Description))
		}
	}
}

func TestResolutionFileCannotReadOutsideLane(t *testing.T) {
	session, r, s := connect(t, Options{})
	out := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "one"}}, Projects: []string{"A"}})
	if out.ExitCode != 0 {
		t.Fatal(out)
	}
	lane := out.Report.Workspace.Path
	if err := os.WriteFile(filepath.Join(lane, "A", "a.txt"), []byte("local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	r.Write("A/a.txt", "upstream\n")
	r.Commit("upstream")
	out = call(t, session, "sync", Mutation{Lane: Lane{Name: "one"}})
	if out.ExitCode != 1 || len(out.Report.Conflicts) != 1 {
		t.Fatal(out)
	}
	token := out.Report.Conflicts[0].ID
	out = call(t, session, "resolve", Resolve{Mutation: Mutation{Lane: Lane{Name: "one"}}, Path: "A/a.txt", Token: token, Mode: "file", FromFile: "../repository/AGENTS.md"})
	if out.ExitCode == 0 {
		t.Fatal("outside read allowed")
	}
	status, err := s.engine.Status(context.Background(), "one")
	if err != nil || len(status.Conflicts) != 1 {
		t.Fatal("failed resolution altered conflicts", err)
	}
	if err = os.WriteFile(filepath.Join(lane, "merged.txt"), []byte("resolved\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out = call(t, session, "resolve", Resolve{Mutation: Mutation{Lane: Lane{Name: "one"}}, Path: "A/a.txt", Token: token, Mode: "file", FromFile: "merged.txt"})
	if out.ExitCode != 0 {
		t.Fatalf("resolution: %+v", out)
	}
}

func TestExpansionPreviewReportsSourceBytes(t *testing.T) {
	_, r, s := connect(t, Options{})
	r.Write("B/data.txt", "additional source\n")
	r.Commit("additional project")
	if _, err := s.engine.Create(context.Background(), "one", workspace.CreateOptions{Projects: []string{"A"}}, false); err != nil {
		t.Fatal(err)
	}
	preview, err := s.engine.Expand(context.Background(), "one", []string{"B"}, true)
	if err != nil || preview.Plan == nil || preview.Plan.SourceBytes != int64(len("additional source\n")) {
		t.Fatalf("incorrect expansion storage estimate: %+v %v", preview.Plan, err)
	}
}

func TestAliasedProjectValidationUsesManifestPath(t *testing.T) {
	session, r, _ := connect(t, Options{})
	r.Write(".workcopy.json", `{"schemaVersion":1,"discovery":{"adapters":["dotnet"]},"projects":[{"id":"api","path":"A","kind":"dotnet"}]}`)
	r.Commit("aliased project")
	out := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "one"}}, Projects: []string{"api"}})
	if out.ExitCode != 0 {
		t.Fatal(out)
	}
	out = call(t, session, "lane_context", Lane{Name: "one"})
	if out.ExitCode != 0 || len(out.Context.Validation) != 1 || len(out.Context.Validation[0].Command) < 3 || out.Context.Validation[0].Command[2] != "A/A.csproj" {
		t.Fatalf("ID used as a filename: %+v", out.Context)
	}
}

func TestBusyCancellationAndShareConsent(t *testing.T) {
	session, r, s := connect(t, Options{})
	if !s.beginMutation(s.engine.WorkRoot) {
		t.Fatal("fixture mutation lock unavailable")
	}
	out := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "one"}}, Projects: []string{"A"}})
	s.endMutation(s.engine.WorkRoot)
	if out.ExitCode != 3 || out.Error.Code != "workspace-busy" {
		t.Fatal("busy writer not reported", out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "workcopy_create", Arguments: Create{Mutation: Mutation{Lane: Lane{Name: "one"}}, Projects: []string{"A"}}}); err == nil {
		t.Fatal("cancelled request accepted")
	}
	out = call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "one"}}, Projects: []string{"A"}})
	if out.ExitCode != 0 {
		t.Fatal(out)
	}
	target := filepath.Join(r.Parent, "dependency-target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	args := Share{Mutation: Mutation{Lane: Lane{Name: "one"}}, Path: "packages", Target: target, DependencyIdentity: "fixture-v1", TrustShared: true}
	out = call(t, session, "share", args)
	if out.ExitCode != 3 || out.Error.Code != "share-target-not-allowed" {
		t.Fatal("implicit target permitted", out)
	}
	s.opts.ShareTargets = []string{target}
	args.TrustShared = false
	out = call(t, session, "share", args)
	if out.ExitCode != 2 && out.ExitCode != 3 {
		t.Fatal("sharing without consent", out)
	}
	args.TrustShared = true
	out = call(t, session, "share", args)
	if out.ExitCode != 0 {
		t.Fatal(out)
	}
	out = call(t, session, "unshare", Solution{Mutation: Mutation{Lane: Lane{Name: "one"}}, Path: "packages"})
	if out.ExitCode != 0 {
		t.Fatal(out)
	}
	if body, err := os.ReadFile(filepath.Join(target, "keep")); err != nil || string(body) != "retained" {
		t.Fatal("shared target damaged", err)
	}
}

func TestInputFramesAreBoundedWithoutLeakingBodies(t *testing.T) {
	input := &boundedInput{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("x", maxRequestBytes+1)))}
	_, err := io.ReadAll(input)
	if err == nil || strings.Contains(err.Error(), "xxx") {
		t.Fatal("oversized frame not safely rejected", err)
	}
	input = &boundedInput{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("x", maxRequestBytes) + "\n" + strings.Repeat("y", maxRequestBytes) + "\n"))}
	if _, err = io.ReadAll(input); err != nil {
		t.Fatal("valid independent frames rejected", err)
	}
}

func TestEmptyCollectionsAndNoCommandCandidatesMatchSchema(t *testing.T) {
	session, r, _ := connect(t, Options{})
	r.Write("tools/package.json", `{"name":"offline-tool","scripts":{}}`)
	r.Commit("node manifest")
	out := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "node"}}, Projects: []string{"tools"}})
	if out.ExitCode != 0 {
		t.Fatalf("node lane creation failed: exit=%d error=%+v report=%+v", out.ExitCode, out.Error, out.Report)
	}
	out = call(t, session, "lane_context", Lane{Name: "node"})
	if out.ExitCode != 0 || len(out.Context.Validation) != 1 || len(out.Context.Validation[0].Command) != 0 {
		t.Fatal(out)
	}
	out = call(t, session, "review", Mutation{Lane: Lane{Name: "node"}, DryRun: true})
	if out.ExitCode != 0 {
		t.Fatal("empty plan failed schema", out)
	}
}
