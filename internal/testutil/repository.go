// Package testutil creates disposable repositories for integration tests.
package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type Repository struct {
	T            testing.TB
	Parent, Path string
}

func NewRepository(t testing.TB) *Repository {
	t.Helper()
	parent, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		parent = resolved
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, ".workcopy-fixture-owner"), []byte("Workcopy test fixture root\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r := &Repository{T: t, Parent: parent, Path: filepath.Join(parent, "repository")}
	if err := os.Mkdir(r.Path, 0700); err != nil {
		t.Fatal(err)
	}
	r.Git("init", "-b", "main")
	r.Git("config", "user.name", "Fixture Author")
	r.Git("config", "user.email", "fixture@example.invalid")
	r.Git("config", "core.autocrlf", "false")
	return r
}

// NewFixtureRepository copies a checked-in repository fixture into a disposable
// directory, then initializes and commits it as a real local Git repository.
func NewFixtureRepository(t testing.TB, name string) *Repository {
	t.Helper()
	r := NewRepository(t)
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate repository fixture root")
	}
	repositoryRoot := filepath.Dir(filepath.Dir(filepath.Dir(sourceFile)))
	if !filepath.IsAbs(sourceFile) {
		directory, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		repositoryRoot, err = sourceRootFromDirectory(directory)
		if err != nil {
			t.Fatal(err)
		}
	}
	fixtureRoot := filepath.Join(repositoryRoot, "tests", "fixtures", name)
	if err := filepath.WalkDir(fixtureRoot, func(source string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(fixtureRoot, source)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return fmt.Errorf("fixture must be source-only and must not contain .git")
		}
		destination := filepath.Join(r.Path, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("fixture contains unsupported file type: %s", relative)
		}
		body, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, body, 0644)
	}); err != nil {
		t.Fatalf("copy fixture %q: %v", name, err)
	}
	r.Commit("checked-in " + name + " fixture")
	return r
}

func sourceRootFromDirectory(directory string) (string, error) {
	for depth := 0; depth < 4; depth++ {
		module, err := os.ReadFile(filepath.Join(directory, "go.mod"))
		if err == nil && strings.TrimSpace(strings.SplitN(string(module), "\n", 2)[0]) == "module workcopy" {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return "", fmt.Errorf("cannot locate Workcopy source root for trimmed-path fixture tests")
}

func (r *Repository) Git(arguments ...string) string {
	r.T.Helper()
	command := exec.Command("git", append([]string{"-C", r.Path}, arguments...)...)
	environment := []string{}
	for _, v := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(v, "=", 2)[0])
		if !strings.HasPrefix(key, "GIT_") {
			environment = append(environment, v)
		}
	}
	command.Env = append(environment, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	body, err := command.CombinedOutput()
	if err != nil {
		r.T.Fatalf("git %v: %v\n%s", arguments, err, body)
	}
	return strings.TrimSpace(string(body))
}
func (r *Repository) Write(name, body string) {
	r.T.Helper()
	p := filepath.Join(r.Path, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		r.T.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		r.T.Fatal(err)
	}
}
func (r *Repository) Commit(message string) string {
	r.T.Helper()
	r.Git("add", "--all")
	r.Git("commit", "-m", message)
	return r.Git("rev-parse", "HEAD")
}
