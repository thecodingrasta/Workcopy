package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workcopy/internal/testutil"
)

const buildOutputConfig = `{"schemaVersion":1,"discovery":{"adapters":["generic"]},"destroy":{"discard":["**/bin/**","**/obj/**",".vs/**"]}}`

func buildOutputRepository(t *testing.T, committedConfig string) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write("src/App/Program.cs", "class P {}\n")
	r.Write("tools/bin/run.sh", "tracked file that merely lives under a bin directory\n")
	if committedConfig != "" {
		r.Write(".workcopy.json", committedConfig)
	}
	r.Commit("base")
	return r
}

func buildOutput(t *testing.T, lane string) {
	t.Helper()
	for _, name := range []string{"src/App/bin/Debug/App.dll", "src/App/obj/project.assets.json", ".vs/state.bin"} {
		path := filepath.Join(lane, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("build output\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// WC-008: a built lane can be cleaned up without --force when the owner has
// declared build output disposable, and only then.
func TestOwnerDeclaredBuildOutputIsDiscardedWithoutForce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := buildOutputRepository(t, "")
	config := filepath.Join(r.Parent, "owner.json")
	if err := os.WriteFile(config, []byte(buildOutputConfig), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}

	// Control: with no discard policy the same lane is refused.
	plain, err := e.Create(ctx, "plain", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	buildOutput(t, plain.Workspace.Path)
	if _, err = e.Destroy(ctx, "plain", false, false); failureCode(t, err) != "unexported-local-file" {
		t.Fatalf("without a policy build output must still block deletion: %v", err)
	}

	owned, err := e.Create(ctx, "owned", CreateOptions{All: true, ConfigPath: config}, false)
	if err != nil {
		t.Fatal(err)
	}
	lane := owned.Workspace.Path
	buildOutput(t, lane)
	planned, err := e.Destroy(ctx, "owned", false, true)
	if err != nil || !strings.Contains(strings.Join(planned.Plan.Effects, "\n"), "Discard 3 local-only files") {
		t.Fatalf("the preview must say what will be discarded: %+v %v", planned.Plan, err)
	}
	if _, err = os.Stat(lane); err != nil {
		t.Fatal("dry run removed the lane")
	}

	// A tracked file under a bin directory is source, not output.
	if err = os.WriteFile(filepath.Join(lane, "tools", "bin", "run.sh"), []byte("edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Destroy(ctx, "owned", false, false); failureCode(t, err) != "unexported-changes" {
		t.Fatalf("an edit to tracked source must still block deletion: %v", err)
	}
	if err = os.WriteFile(filepath.Join(lane, "tools", "bin", "run.sh"), []byte("tracked file that merely lives under a bin directory\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Anything the policy does not name is still protected.
	if err = os.WriteFile(filepath.Join(lane, "notes.txt"), []byte("mine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Destroy(ctx, "owned", false, false); failureCode(t, err) != "unexported-local-file" {
		t.Fatalf("an unrelated local file must still block deletion: %v", err)
	}
	if err = os.Remove(filepath.Join(lane, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Destroy(ctx, "owned", false, false); err != nil {
		t.Fatalf("build output declared disposable must not need --force: %v", err)
	}
	if _, err = os.Stat(lane); !os.IsNotExist(err) {
		t.Fatal("lane remains")
	}
}

// A repository must not be able to grant itself permission to discard files.
func TestCommittedDiscardPolicyIsIgnoredAndMatchEverythingIsRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := buildOutputRepository(t, buildOutputConfig)
	e, err := New(ctx, r.Path, filepath.Join(r.Parent, "lanes"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Create(ctx, "task", CreateOptions{All: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	buildOutput(t, created.Workspace.Path)
	if _, err = e.Destroy(ctx, "task", false, false); failureCode(t, err) != "unexported-local-file" {
		t.Fatalf("a committed discard policy must not be honoured: %v", err)
	}
	planned, err := e.Destroy(ctx, "task", true, true)
	if err != nil || strings.Contains(strings.Join(planned.Warnings, "\n"), "ignored") == false {
		t.Fatalf("the user should be told the committed policy is ignored: %+v %v", planned.Warnings, err)
	}

	for _, pattern := range []string{`**`, `*`, `/`, `.`, `../x`, `/abs/**`, `a\\b`} {
		bad := filepath.Join(r.Parent, "bad.json")
		if err = os.WriteFile(bad, []byte(`{"schemaVersion":1,"destroy":{"discard":["`+pattern+`"]}}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = e.Create(ctx, "bad", CreateOptions{All: true, ConfigPath: bad}, true); failureCode(t, err) != "invalid-discard-pattern" {
			t.Errorf("pattern %q must be rejected, got %v", pattern, err)
		}
	}
}
