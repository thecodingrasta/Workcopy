package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"workcopy/internal/model"
	"workcopy/internal/testutil"
)

// WC-002: the Git program must be selectable, because on WSL the first git on
// PATH is not necessarily the one that owns the checkout.
func TestWorkcopyGitSelectsTheProgramAndFailsClosed(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	fixture := testutil.NewRepository(t)
	fixture.Write("a", "x")
	fixture.Commit("base")

	t.Setenv(ExecutableEnvironment, real)
	repository, err := Open(context.Background(), fixture.Path)
	if err != nil || repository.Executable != real {
		t.Fatalf("override not used: %+v %v", repository, err)
	}

	// A wrapper proves the override is really the program that runs.
	if runtime.GOOS != "windows" {
		marker := filepath.Join(t.TempDir(), "ran")
		wrapper := filepath.Join(t.TempDir(), "wrapped-git")
		script := "#!/bin/sh\necho ran >> '" + marker + "'\nexec '" + real + "' \"$@\"\n"
		if err = os.WriteFile(wrapper, []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(ExecutableEnvironment, wrapper)
		if _, err = Open(context.Background(), fixture.Path); err != nil {
			t.Fatal(err)
		}
		if body, readErr := os.ReadFile(marker); readErr != nil || !strings.Contains(string(body), "ran") {
			t.Fatal("WORKCOPY_GIT program was not the one executed")
		}
	}

	t.Setenv(ExecutableEnvironment, filepath.Join(t.TempDir(), "no-such-git"))
	_, err = Open(context.Background(), fixture.Path)
	var failure *model.Failure
	if !errors.As(err, &failure) || failure.Code != "git-unavailable" || !strings.Contains(failure.Message, ExecutableEnvironment) {
		t.Fatalf("a bad override must fail closed and name the variable, got %v", err)
	}
}

func TestCheckoutConfigReadsTheUsersLineEndingSettings(t *testing.T) {
	fixture := testutil.NewRepository(t)
	fixture.Write("a", "x")
	fixture.Commit("base")
	repository, err := Open(context.Background(), fixture.Path)
	if err != nil {
		t.Fatal(err)
	}
	// NewRepository pins core.autocrlf=false locally. git init also writes
	// core.filemode=false on Windows and on filesystems that cannot store
	// modes, so the default for that key is covered by the explicit cases below.
	config, err := repository.CheckoutConfig(context.Background())
	if err != nil || config.AutoCRLF != "false" || config.EOL != "native" {
		t.Fatalf("defaults: %+v %v", config, err)
	}
	for _, c := range []struct {
		key, value string
		check      func(CheckoutConfig) bool
	}{
		{"core.autocrlf", "true", func(c CheckoutConfig) bool { return c.AutoCRLF == "true" }},
		{"core.autocrlf", "input", func(c CheckoutConfig) bool { return c.AutoCRLF == "input" }},
		{"core.autocrlf", "no", func(c CheckoutConfig) bool { return c.AutoCRLF == "false" }},
		{"core.eol", "crlf", func(c CheckoutConfig) bool { return c.EOL == "crlf" }},
		{"core.eol", "lf", func(c CheckoutConfig) bool { return c.EOL == "lf" }},
		{"core.eol", "native", func(c CheckoutConfig) bool { return c.EOL == "native" }},
		{"core.fileMode", "false", func(c CheckoutConfig) bool { return !c.FileMode }},
		{"core.fileMode", "true", func(c CheckoutConfig) bool { return c.FileMode }},
	} {
		fixture.Git("config", c.key, c.value)
		got, readErr := repository.CheckoutConfig(context.Background())
		if readErr != nil || !c.check(got) {
			t.Errorf("%s=%s read as %+v (%v)", c.key, c.value, got, readErr)
		}
	}
}
