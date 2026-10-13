// Command dev provides the same contributor tasks on every supported platform.
package main

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const outputMarker = "Workcopy build outputs\n"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, arguments []string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("usage: go run ./tools/dev setup|build|install|test|validate|format|clean|package VERSION [--native]")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	moduleLine := strings.TrimSuffix(strings.SplitN(string(module), "\n", 2)[0], "\r")
	if err != nil || moduleLine != "module workcopy" {
		return fmt.Errorf("run this command from the Workcopy source root")
	}
	if arguments[0] != "package" && len(arguments) != 1 {
		return fmt.Errorf("%s takes no arguments", arguments[0])
	}
	switch arguments[0] {
	case "setup":
		return prerequisites(ctx)
	case "format":
		return formatFiles(root, true)
	case "test":
		return command(ctx, nil, "go", "test", "-count=1", "-timeout=180s", "./...")
	case "build":
		return build(ctx, root, runtime.GOOS, runtime.GOARCH, "dev", binaryName(runtime.GOOS))
	case "install":
		if err = prerequisites(ctx); err != nil {
			return err
		}
		return command(ctx, nil, "go", "install", "-trimpath", "-buildvcs=false", "./cmd/workcopy")
	case "validate":
		if err = prerequisites(ctx); err != nil {
			return err
		}
		if err = formatFiles(root, false); err != nil {
			return err
		}
		if err = command(ctx, nil, "go", "vet", "./..."); err != nil {
			return err
		}
		if err = command(ctx, nil, "go", "test", "-race", "-count=1", "-timeout=180s", "./..."); err != nil {
			return err
		}
		if err = build(ctx, root, runtime.GOOS, runtime.GOARCH, "dev", binaryName(runtime.GOOS)); err != nil {
			return err
		}
		executable := filepath.Join(root, "dist", binaryName(runtime.GOOS))
		for _, argument := range []string{"version", "doctor", "help"} {
			if err = command(ctx, nil, executable, argument, "--json"); err != nil {
				return err
			}
		}
		return nil
	case "clean":
		return clean(root)
	case "package":
		if len(arguments) < 2 || len(arguments) > 3 || !validVersion(arguments[1]) || len(arguments) == 3 && arguments[2] != "--native" {
			return fmt.Errorf("package requires VERSION [--native], such as v0.1.0-rc.1")
		}
		if !modernGo(runtime.Version()) {
			return fmt.Errorf("release packaging requires Go 1.25 or newer; use a currently supported patched release")
		}
		return release(ctx, root, arguments[1], len(arguments) == 3)
	default:
		return fmt.Errorf("unknown task %q", arguments[0])
	}
}
func command(ctx context.Context, environment []string, executable string, arguments ...string) error {
	fmt.Printf("+ %s %s\n", executable, strings.Join(arguments, " "))
	process := exec.CommandContext(ctx, executable, arguments...)
	process.Env = environment
	process.Stdout, process.Stderr = os.Stdout, os.Stderr
	return process.Run()
}
func prerequisites(ctx context.Context) error {
	if err := command(ctx, nil, "go", "version"); err != nil {
		return err
	}
	body, err := exec.CommandContext(ctx, "git", "--version").Output()
	if err != nil {
		return fmt.Errorf("Git 2.43 or newer is required on PATH: %w", err)
	}
	fmt.Print(string(body))
	numbers := regexp.MustCompile(`git version (\d+)\.(\d+)`).FindStringSubmatch(string(body))
	if len(numbers) != 3 {
		return fmt.Errorf("cannot determine Git version")
	}
	major, _ := strconv.Atoi(numbers[1])
	minor, _ := strconv.Atoi(numbers[2])
	if major < 2 || major == 2 && minor < 43 {
		return fmt.Errorf("Git 2.43 or newer is required")
	}
	if !modernGo(runtime.Version()) {
		fmt.Println("Compatibility build: upgrade to a supported Go release before distributing binaries.")
	}
	return nil
}
func modernGo(version string) bool {
	parts := regexp.MustCompile(`^go(\d+)\.(\d+)`).FindStringSubmatch(version)
	if len(parts) != 3 {
		return false
	}
	major, _ := strconv.Atoi(parts[1])
	minor, _ := strconv.Atoi(parts[2])
	return major > 1 || major == 1 && minor >= 25
}
func binaryName(goos string) string {
	if goos == "windows" {
		return "workcopy.exe"
	}
	return "workcopy"
}
func prepareDist(root string) error {
	directory := filepath.Join(root, "dist")
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		if err = os.Mkdir(directory, 0755); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("dist must be a real directory")
	}
	marker := filepath.Join(directory, ".workcopy-build")
	if info, err = os.Lstat(marker); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("invalid output ownership marker")
		}
		body, err := os.ReadFile(marker)
		if err != nil || string(body) != outputMarker {
			return fmt.Errorf("unrecognized output ownership marker")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("dist contains unowned files; choose a clean source checkout")
	}
	return os.WriteFile(marker, []byte(outputMarker), 0600)
}
func build(ctx context.Context, root, goos, goarch, version, name string) error {
	if err := prepareDist(root); err != nil {
		return err
	}
	destination := filepath.Join(root, "dist", name)
	if info, err := os.Lstat(destination); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("build output is not a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Join(root, "dist"), ".workcopy-build-*")
	if err != nil {
		return err
	}
	temporary.Close()
	defer os.Remove(temporary.Name())
	environment := []string{}
	for _, value := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
		if key != "GOOS" && key != "GOARCH" && key != "CGO_ENABLED" {
			environment = append(environment, value)
		}
	}
	environment = append(environment, "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if err = command(ctx, environment, "go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-X workcopy/internal/cli.Version="+version, "-o", temporary.Name(), "./cmd/workcopy"); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), destination)
}
func formatFiles(root string, write bool) error {
	changed := []string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "dist", "vendor", ".cache", ".codex", ".gitnexus", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(name) != ".go" {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("Go source must not be a symlink: %s", name)
		}
		body, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		formatted, err := format.Source(body)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if !bytes.Equal(body, formatted) {
			changed = append(changed, name)
			if write {
				return os.WriteFile(name, formatted, 0644)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !write && len(changed) > 0 {
		return fmt.Errorf("run go run ./tools/dev format; unformatted files: %s", strings.Join(changed, ", "))
	}
	return nil
}
func clean(root string) error {
	directory := filepath.Join(root, "dist")
	if _, err := os.Lstat(directory); os.IsNotExist(err) {
		return nil
	}
	if err := prepareDist(root); err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !(name == ".workcopy-build" || name == "workcopy" || name == "workcopy.exe" || name == "SHA256SUMS" || assetName.MatchString(name) || sourceAssetName.MatchString(name)) {
			return fmt.Errorf("refusing to remove unrecognized build output %q", name)
		}
	}
	for _, entry := range entries {
		if err = os.Remove(filepath.Join(directory, entry.Name())); err != nil {
			return err
		}
	}
	return os.Remove(directory)
}
