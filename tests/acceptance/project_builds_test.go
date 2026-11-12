package acceptance

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"workcopy/internal/testutil"
)

// Every project must be able to build, test and validate in its own lanes,
// several at the same moment, without a helper script and without the lanes or
// the main checkouts disturbing each other. The toolchain used is Go's, because
// it is present wherever these tests run; the point is the isolation, not Go.

func goProject(t *testing.T, name string) *testutil.Repository {
	t.Helper()
	r := testutil.NewRepository(t)
	r.Write("go.mod", "module example.com/"+name+"\n\ngo 1.21\n")
	r.Write("main.go", fmt.Sprintf("package main\n\nimport \"fmt\"\n\nfunc Name() string { return %q }\n\nfunc main() { fmt.Println(Name()) }\n", name+"-v1"))
	r.Write("main_test.go", fmt.Sprintf("package main\n\nimport \"testing\"\n\nfunc TestName(t *testing.T) {\n\tif Name() != %q {\n\t\tt.Fatalf(\"got %%q\", Name())\n\t}\n}\n", name+"-v1"))
	r.Write(".gitignore", "bin/\n")
	r.Commit(name)
	return r
}

// inLane runs a program with a lane as the working directory, as a build script
// that asks `workcopy path` does.
func inLane(t *testing.T, lane string, program string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, program, args...)
	command.Dir = lane
	// A lane has no .git; the build must not wander up into some other checkout.
	command.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	err := command.Run()
	return output.String(), err
}

func TestEachProjectBuildsAndTestsInItsOwnLanes(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the Go toolchain is not on PATH")
	}
	t.Parallel()
	names := []string{"alpha", "beta"}
	repositories := map[string]*testutil.Repository{}
	for _, name := range names {
		r := goProject(t, name)
		repositories[name] = r
		// Owner configuration per project through the project's own settings file.
		config := filepath.Join(r.Parent, name+"-owner.json")
		if err := os.WriteFile(config, []byte(`{"schemaVersion":1,"destroy":{"discard":["bin/**"]}}`), 0600); err != nil {
			t.Fatal(err)
		}
		writeProjectSettings(t, r, "WORKCOPY_CONFIG="+config+"\n")
	}

	type lane struct{ project, name, path, version string }
	var lanes []*lane
	for _, project := range names {
		r := repositories[project]
		for n, name := range []string{"one", "two"} {
			created := laneReport(t, r.Path, nil, 0, "create", name, "--all")
			version := "v1"
			if n == 1 {
				version = "v2"
				// An edit in this lane only.
				for _, file := range []string{"main.go", "main_test.go"} {
					full := filepath.Join(created.Workspace.Path, file)
					body, err := os.ReadFile(full)
					if err != nil {
						t.Fatal(err)
					}
					if err = os.WriteFile(full, bytes.ReplaceAll(body, []byte(project+"-v1"), []byte(project+"-v2")), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			// The path a script would use, asked of Workcopy and not assumed.
			stdout, _, code := run(t, r.Path, nil, "path", name)
			if code != 0 || strings.TrimSpace(stdout) != created.Workspace.Path {
				t.Fatalf("path %s/%s: %d %q", project, name, code, stdout)
			}
			lanes = append(lanes, &lane{project, name, strings.TrimSpace(stdout), version})
		}
	}

	// Build and test all four lanes at once.
	binary := "app"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	var group sync.WaitGroup
	for _, l := range lanes {
		group.Add(1)
		go func() {
			defer group.Done()
			if output, err := inLane(t, l.path, "go", "build", "-o", filepath.Join("bin", binary), "."); err != nil {
				t.Errorf("%s/%s build: %v\n%s", l.project, l.name, err, output)
				return
			}
			if output, err := inLane(t, l.path, "go", "test", "./..."); err != nil {
				t.Errorf("%s/%s test: %v\n%s", l.project, l.name, err, output)
				return
			}
			output, err := inLane(t, l.path, filepath.Join(l.path, "bin", binary))
			if want := l.project + "-" + l.version; err != nil || strings.TrimSpace(output) != want {
				t.Errorf("%s/%s binary printed %q (%v), want %q: it must be this lane's own build", l.project, l.name, output, err, want)
			}
		}()
	}
	group.Wait()

	for _, project := range names {
		r := repositories[project]
		// The main checkout was never built in.
		if _, err := os.Stat(filepath.Join(r.Path, "bin")); !os.IsNotExist(err) {
			t.Errorf("%s: a lane's build output reached the main checkout", project)
		}
		// Status sees the source edit and not the build output.
		one := laneReport(t, r.Path, nil, 0, "status", "one")
		two := laneReport(t, r.Path, nil, 0, "status", "two")
		if len(one.Changes) != 0 {
			t.Errorf("%s/one: build output must not look like a change: %+v", project, one.Changes)
		}
		if got := changedPaths(two); strings.Join(got, ",") != "main.go,main_test.go" {
			t.Errorf("%s/two: want exactly the two edited files, got %v", project, got)
		}
		// Each lane is cleaned up with the project's own discard rule, without
		// --force: build output of any size is discardable.
		laneReport(t, r.Path, nil, 0, "destroy", "one")
	}
	// The other project's lanes were untouched by those cleanups.
	for _, l := range lanes {
		if l.name == "two" {
			if _, err := os.Stat(filepath.Join(l.path, "bin", binary)); err != nil {
				t.Errorf("%s/two lost its build: %v", l.project, err)
			}
		}
	}
}
