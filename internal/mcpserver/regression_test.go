package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

type progressEvent struct {
	message     string
	done, total float64
}

// connectWithProgress is connect with a client that records progress
// notifications, as a real MCP client does when it wants to show activity.
func connectWithProgress(t *testing.T, opts Options) (*mcp.ClientSession, *testutil.Repository, func() []progressEvent) {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write("A/A.csproj", `<Project/>`)
	r.Write("A/a.txt", "base\n")
	r.Commit("fixture")
	e, err := workspace.New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(e, "test", opts)
	var mu sync.Mutex
	var events []progressEvent
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	go func() { _ = s.Run(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, request *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, progressEvent{request.Params.Message, request.Params.Progress, request.Params.Total})
		},
	})
	observe(client)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, r, func() []progressEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]progressEvent(nil), events...)
	}
}

func callWithToken(t *testing.T, session *mcp.ClientSession, name string, args any) Output {
	t.Helper()
	params := &mcp.CallToolParams{Name: "workcopy_" + name, Arguments: args}
	params.SetProgressToken("progress-" + name)
	result, err := session.CallTool(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result.StructuredContent)
	var out Output
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// WC-011: a client that supplies a progress token sees creation and review
// advance; one that does not gets no notifications.
func TestLongOperationsReportProgressOnlyWhenAsked(t *testing.T) {
	session, _, events := connectWithProgress(t, Options{})
	if out := callWithToken(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "worker"}}, Projects: []string{"A"}}); out.ExitCode != 0 {
		t.Fatalf("create: %+v", out.Error)
	}
	if err := os.WriteFile(filepath.Join(sessionLanePath(t, session, "worker"), "A", "a.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if out := callWithToken(t, session, "review", Mutation{Lane: Lane{Name: "worker"}}); out.ExitCode != 0 {
		t.Fatalf("review: %+v", out.Error)
	}
	seen := map[string]bool{}
	for _, event := range events() {
		seen[event.message] = true
		if event.total <= 0 || event.done < 0 || event.done > event.total {
			t.Errorf("implausible progress %+v", event)
		}
	}
	if !seen["Creating lane files"] || !seen["Preparing review files"] {
		t.Fatalf("expected creation and review progress, got %v", seen)
	}

	before := len(events())
	if out := call(t, session, "status", Lane{Name: "worker"}); out.ExitCode != 0 {
		t.Fatal(out.Error)
	}
	if len(events()) != before {
		t.Fatal("a call without a progress token must not produce notifications")
	}
}

func sessionLanePath(t *testing.T, session *mcp.ClientSession, name string) string {
	t.Helper()
	out := call(t, session, "status", Lane{Name: name})
	if out.Report == nil || out.Report.Workspace == nil {
		t.Fatalf("status gave no workspace: %+v", out)
	}
	return out.Report.Workspace.Path
}

// WC-011: when the deadline passes, the agent is told the work is recoverable
// and how to lift the limit, instead of a bare "context deadline exceeded".
func TestDeadlineExpiryExplainsRecoveryAndTheTimeoutFlag(t *testing.T) {
	session, _, _ := connect(t, Options{ToolTimeout: time.Nanosecond})
	out := call(t, session, "bootstrap", Bootstrap{})
	if out.ExitCode != 3 || out.Error == nil || out.Error.Code != "cancelled" {
		t.Fatalf("deadline expiry must map to cancelled/3: %+v", out)
	}
	if !strings.Contains(out.Error.Suggestion, "workcopy_recover") || !strings.Contains(out.Error.Suggestion, "--tool-timeout") {
		t.Fatalf("suggestion must name recovery and the flag: %q", out.Error.Suggestion)
	}
}

func TestToolTimeoutFlagIsBounded(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("a", "x")
	r.Commit("base")
	for _, c := range []struct {
		value string
		code  int
	}{{"1s", 2}, {"25h", 2}, {"10s", 0}, {"15m", 0}, {"24h", 0}} {
		var stderr strings.Builder
		code := Command(context.Background(), []string{"--repo", r.Path, "--root", filepath.Join(r.Parent, "lanes"), "--tool-timeout", c.value}, "test", io.NopCloser(strings.NewReader("")), discardCloser{io.Discard}, &stderr)
		if code != c.code {
			t.Errorf("--tool-timeout %s: exit %d, want %d (%s)", c.value, code, c.code, stderr.String())
		}
	}
}

// WC-015 through MCP: an agent sees the lane as missing and can clear it, and
// cannot be led into deleting anything else.
func TestMissingLaneIsVisibleAndClearableOverMCP(t *testing.T) {
	session, _, _ := connect(t, Options{})
	created := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "worker"}}, Projects: []string{"A"}})
	if created.ExitCode != 0 {
		t.Fatal(created.Error)
	}
	if err := os.RemoveAll(created.Report.Workspace.Path); err != nil {
		t.Fatal(err)
	}
	listed := call(t, session, "list", Empty{})
	if len(listed.Report.Workspaces) != 1 || listed.Report.Workspaces[0].Phase != "missing" || listed.Report.Workspaces[0].Branch != "refs/heads/main" {
		t.Fatalf("list must show a missing lane with its branch: %+v", listed.Report.Workspaces)
	}
	for _, tool := range []string{"status", "lane_context"} {
		if out := call(t, session, tool, Lane{Name: "worker"}); out.ExitCode != 3 || out.Error == nil || out.Error.Code != "workspace-missing" || !strings.Contains(out.Error.Suggestion, "destroy") {
			t.Fatalf("%s on a missing lane: %+v", tool, out)
		}
	}
	if out := call(t, session, "recover", Mutation{Lane: Lane{Name: "worker"}, DryRun: true}); out.Error == nil || out.Error.Code != "workspace-missing" {
		t.Fatalf("recover must agree with status: %+v", out)
	}
	if out := call(t, session, "destroy", Mutation{Lane: Lane{Name: "worker"}, DryRun: true}); out.ExitCode != 0 {
		t.Fatalf("destroy preview: %+v", out.Error)
	}
	if out := call(t, session, "destroy", Mutation{Lane: Lane{Name: "worker"}}); out.ExitCode != 0 {
		t.Fatalf("destroy must clear the registration without force: %+v", out.Error)
	}
	if listed = call(t, session, "list", Empty{}); len(listed.Report.Workspaces) != 0 {
		t.Fatalf("registration survived: %+v", listed.Report.Workspaces)
	}
}

// WC-012 / WC-002 / WC-006 over MCP: a path that cannot exist on this platform is
// refused with a message that names the fix, and the doctor report is reachable
// by an agent. (WC-022: the other platform's spelling of a path that can exist
// is accepted; TestWSLSpellingIsAcceptedAndAnsweredInKind covers that.)
func TestForeignPathsAndDoctorOverMCP(t *testing.T) {
	session, _, _ := connect(t, Options{})
	unreachable, code := "/home/me/repo", "wsl-path-unreachable"
	if runtime.GOOS != "windows" {
		// A UNC share is refused outside WSL and inside it alike, unlike C:\ which
		// WSL can reach through its drive mount.
		unreachable, code = `\\server\share\repo`, "windows-path-on-posix"
	}
	out := call(t, session, "bootstrap", Bootstrap{Directory: unreachable})
	if out.ExitCode != 2 || out.Error == nil || out.Error.Code != code || !strings.Contains(out.Error.Message, "path") {
		t.Fatalf("an unreachable directory must be rejected with %s and a usable message: %+v", code, out.Error)
	}
	scoped := call(t, session, "status", Lane{Name: "x", Scope: Scope{Repo: unreachable}})
	if scoped.ExitCode != 2 || scoped.Error == nil || scoped.Error.Code != code {
		t.Fatalf("an unreachable repo scope must be rejected with %s: %+v", code, scoped.Error)
	}

	doctor := call(t, session, "doctor", Empty{})
	if doctor.ExitCode != 0 {
		t.Fatal(doctor.Error)
	}
	for _, want := range []string{"Git:", "Line endings:", "Repository:"} {
		if !strings.Contains(doctor.Report.Message, want) {
			t.Errorf("doctor message lacks %q:\n%s", want, doctor.Report.Message)
		}
	}
}

// The server reads the same settings file as the CLI: a preference set once is
// used, an environment variable beats it, and a flag beats both.
func TestCommandReadsThePersistentSettings(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("a", "x")
	r.Commit("base")
	file := filepath.Join(r.Parent, "workcopy.env")
	start := func(body string, environment map[string]string, extra ...string) (int, string) {
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("WORKCOPY_SETTINGS", file)
		for key, value := range environment {
			t.Setenv(key, value)
		}
		var stderr strings.Builder
		args := append([]string{"--repo", r.Path, "--root", filepath.Join(r.Parent, "lanes")}, extra...)
		code := Command(context.Background(), args, "test", io.NopCloser(strings.NewReader("")), discardCloser{io.Discard}, &stderr)
		return code, stderr.String()
	}
	if code, msg := start("WORKCOPY_TOOL_TIMEOUT=45m\nWORKCOPY_MAX_LANES=5\n", nil); code != 0 {
		t.Fatalf("a valid persistent timeout must start the server: %d %s", code, msg)
	}
	if code, msg := start("WORKCOPY_TOOL_TIMEOUT=5s\n", nil); code != 2 || !strings.Contains(msg, "WORKCOPY_TOOL_TIMEOUT") {
		t.Fatalf("an out-of-range persistent value is reported by name: %d %s", code, msg)
	}
	if code, msg := start("WORKCOPY_TOOL_TIMEOUT=5s\n", map[string]string{"WORKCOPY_TOOL_TIMEOUT": "30m"}); code != 0 {
		t.Fatalf("the environment beats the file: %d %s", code, msg)
	}
	if code, msg := start("WORKCOPY_TOOL_TIMEOUT=5s\n", nil, "--tool-timeout", "30m"); code != 0 {
		t.Fatalf("a flag beats the file: %d %s", code, msg)
	}
	if code, _ := start("WORKCOPY_NOT_A_SETTING=1\n", nil); code != 2 {
		t.Fatal("a typo in the settings file must stop the server, not be ignored")
	}
	if code, _ := start("WORKCOPY_ALLOW_SHARE_TARGET="+r.Parent+";"+r.Path+"\n", nil); code != 0 {
		t.Fatal("share targets can be listed in the file")
	}
}

// workcopy_storage is a read-only report: it returns the ledger for a real lane,
// reports a missing lane as an ordinary failure, and changes nothing.
func TestStorageToolReportsALaneAndIsReadOnly(t *testing.T) {
	session, _, _ := connect(t, Options{})
	created := call(t, session, "create", Create{Mutation: Mutation{Lane: Lane{Name: "one"}}, Projects: []string{"A"}})
	if created.ExitCode != 0 {
		t.Fatal(created)
	}
	lane := created.Report.Workspace.Path
	output := filepath.Join(lane, "A", "bin", "A.dll")
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, make([]byte, 30000), 0o600); err != nil {
		t.Fatal(err)
	}
	generation := created.Report.Generation

	out := call(t, session, "storage", Lane{Name: "one"})
	if out.ExitCode != 0 || out.Report == nil || out.Report.Storage == nil {
		t.Fatalf("storage: %+v", out)
	}
	detail := out.Report.Storage
	likely := detail.Account.Classes["likely-output"]
	if likely == nil || likely.Files != 1 || likely.LogicalBytes != 30000 || likely.ReclaimableBytes == 0 {
		t.Fatalf("likely-output row wrong: %+v", detail.Account.Classes)
	}
	if detail.Volume.FileSystem == "" || detail.Volume.ID == "" {
		t.Fatalf("volume not described: %+v", detail.Volume)
	}
	if !strings.Contains(out.Report.Message, "Read-only") || !strings.Contains(out.Report.Message, "reclaimable") {
		t.Fatalf("message: %s", out.Report.Message)
	}
	if data, err := os.ReadFile(output); err != nil || len(data) != 30000 {
		t.Fatalf("the measured file changed: %d bytes, %v", len(data), err)
	}
	if status := call(t, session, "status", Lane{Name: "one"}); status.Report.Generation != generation {
		t.Fatalf("generation moved from %d to %d", generation, status.Report.Generation)
	}
	if missing := call(t, session, "storage", Lane{Name: "nope"}); missing.ExitCode == 0 || missing.Error == nil {
		t.Fatalf("a missing lane must fail: %+v", missing)
	}
}
