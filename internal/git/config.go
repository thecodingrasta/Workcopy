package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"workcopy/internal/model"
)

// ExecutableEnvironment names the variable that selects the Git program.
// Without it Workcopy uses the first "git" on PATH.
const ExecutableEnvironment = "WORKCOPY_GIT"

// Executable resolves the Git program every Workcopy process uses. The
// override exists for hybrid setups such as WSL, where the first git on PATH
// may not be the one that matches the checkout's owner.
func Executable() (string, error) {
	name := "git"
	if override := os.Getenv(ExecutableEnvironment); override != "" {
		name = override
	}
	path, err := exec.LookPath(name)
	if err != nil {
		if name != "git" {
			return "", model.Fail(3, "git-unavailable", ExecutableEnvironment+" does not name an executable Git program: "+name)
		}
		return "", model.Fail(3, "git-unavailable", "Install Git and put it on PATH")
	}
	return path, nil
}

// CheckoutConfig is the subset of the user's Git configuration that decides
// how Git writes a working tree. Workcopy's own commands force neutral values
// for these keys, so they are read separately and only used to mirror the
// checkout the owner would get from git itself.
type CheckoutConfig struct {
	// AutoCRLF is "true", "input" or "false".
	AutoCRLF string
	// EOL is "lf", "crlf" or "native" (the default).
	EOL string
	// FileMode is core.fileMode; Git defaults to true.
	FileMode bool
}

// CheckoutConfig reads the effective configuration for this repository,
// including system and global files that Workcopy's operational commands
// deliberately ignore.
func (r *Repository) CheckoutConfig(ctx context.Context) (CheckoutConfig, error) {
	result := CheckoutConfig{AutoCRLF: "false", EOL: "native", FileMode: true}
	command := exec.CommandContext(ctx, r.Executable, "--no-pager", "-C", r.Root, "config", "--null", "--get-regexp", `^core\.(autocrlf|eol|filemode)$`)
	command.Env = userEnvironment()
	var output, diagnostics bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostics
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		// Exit status 1 with no diagnostics means "no matching key".
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 && diagnostics.Len() == 0 {
			return result, nil
		}
		return result, fmt.Errorf("git config failed: %w: %s", err, strings.TrimSpace(diagnostics.String()))
	}
	for _, record := range bytes.Split(output.Bytes(), []byte{0}) {
		key, value, found := strings.Cut(string(record), "\n")
		if !found {
			continue
		}
		value = strings.ToLower(strings.TrimSpace(value))
		switch strings.ToLower(key) {
		case "core.autocrlf":
			switch {
			case value == "input":
				result.AutoCRLF = "input"
			case configTrue(value):
				result.AutoCRLF = "true"
			default:
				result.AutoCRLF = "false"
			}
		case "core.eol":
			if value == "lf" || value == "crlf" {
				result.EOL = value
			} else {
				result.EOL = "native"
			}
		case "core.filemode":
			result.FileMode = configTrue(value) || value == ""
		}
	}
	return result, nil
}

func configTrue(value string) bool {
	switch value {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

// userEnvironment keeps the user's Git configuration files in effect while
// still discarding variables that would redirect the repository or index.
func userEnvironment() []string {
	result := []string{}
	for _, value := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
		if key == "GIT_CONFIG_GLOBAL" || key == "GIT_CONFIG_SYSTEM" || key == "GIT_CONFIG_NOSYSTEM" || !strings.HasPrefix(key, "GIT_") {
			result = append(result, value)
		}
	}
	return append(result, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
}

// NativeCRLF reports whether Git's "native" line ending is CRLF for the Git
// program in use, which is true for Windows builds of Git. It is judged by the
// program rather than the host because a Linux process may be told to run
// git.exe through WORKCOPY_GIT.
func (r *Repository) NativeCRLF() bool {
	return runtime.GOOS == "windows" || strings.HasSuffix(strings.ToLower(r.Executable), ".exe")
}
