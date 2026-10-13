package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

var releaseVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$`)
var assetName = regexp.MustCompile(`^workcopy_v[0-9A-Za-z.-]+_(linux|windows|darwin)_(amd64|arm64)\.(zip|tar\.gz)$`)
var sourceAssetName = regexp.MustCompile(`^workcopy_v[0-9A-Za-z.-]+_source\.tar\.gz$`)

func validVersion(value string) bool { return len(value) <= 64 && releaseVersion.MatchString(value) }

type archiveEntry struct {
	Name string
	Data []byte
	Mode fs.FileMode
}

func writeArchive(filename string, entries []archiveEntry) (result error) {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := file.Close()
		if result == nil {
			result = closeErr
		}
		if result != nil {
			os.Remove(filename)
		}
	}()
	epoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if strings.HasSuffix(filename, ".zip") {
		writer := zip.NewWriter(file)
		for _, entry := range entries {
			header := &zip.FileHeader{Name: entry.Name, Method: zip.Deflate, Modified: epoch}
			header.SetMode(entry.Mode)
			member, err := writer.CreateHeader(header)
			if err != nil {
				writer.Close()
				return err
			}
			if _, err = member.Write(entry.Data); err != nil {
				writer.Close()
				return err
			}
		}
		return writer.Close()
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.Name, Size: int64(len(entry.Data)), Mode: int64(entry.Mode), ModTime: epoch}
		if err := tarWriter.WriteHeader(header); err != nil {
			tarWriter.Close()
			gzipWriter.Close()
			return err
		}
		if _, err := tarWriter.Write(entry.Data); err != nil {
			tarWriter.Close()
			gzipWriter.Close()
			return err
		}
	}
	if err = tarWriter.Close(); err != nil {
		gzipWriter.Close()
		return err
	}
	return gzipWriter.Close()
}
func release(ctx context.Context, root, version string, nativeOnly bool) error {
	if err := prepareDist(root); err != nil {
		return err
	}
	notices, err := documentation(root)
	if err != nil {
		return err
	}
	license, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "LICENSE"))
	if err != nil {
		return err
	}
	notices = append(notices, archiveEntry{Name: "GO-LICENSE", Data: license, Mode: 0644})
	sumPath := filepath.Join(root, "dist", "SHA256SUMS")
	if _, err = os.Lstat(sumPath); err == nil {
		return fmt.Errorf("SHA256SUMS exists; clean owned outputs before packaging a release")
	} else if !os.IsNotExist(err) {
		return err
	}
	checksums := []string{}
	source, err := correspondingSource(ctx, root, version)
	if err != nil {
		return err
	}
	sourceName := "workcopy_" + version + "_source.tar.gz"
	if err = writeArchive(filepath.Join(root, "dist", sourceName), source); err != nil {
		return err
	}
	addChecksum := func(name string) error {
		body, err := os.ReadFile(filepath.Join(root, "dist", name))
		if err != nil {
			return err
		}
		digest := sha256.Sum256(body)
		checksums = append(checksums, hex.EncodeToString(digest[:])+"  "+name)
		return nil
	}
	if err = addChecksum(sourceName); err != nil {
		return err
	}
	for _, goos := range []string{"linux", "windows", "darwin"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			if nativeOnly && (goos != runtime.GOOS || goarch != runtime.GOARCH) {
				continue
			}
			name := binaryName(goos)
			if err = build(ctx, root, goos, goarch, version, name); err != nil {
				return err
			}
			binary, err := os.ReadFile(filepath.Join(root, "dist", name))
			if err != nil {
				return err
			}
			entries := append([]archiveEntry{{Name: name, Data: binary, Mode: 0755}}, notices...)
			extension := ".tar.gz"
			if goos == "windows" {
				extension = ".zip"
			}
			filename := fmt.Sprintf("workcopy_%s_%s_%s%s", version, goos, goarch, extension)
			destination := filepath.Join(root, "dist", filename)
			if err = writeArchive(destination, entries); err != nil {
				return err
			}
			if err = addChecksum(filename); err != nil {
				return err
			}
		}
	}
	sort.Strings(checksums)
	return os.WriteFile(sumPath, []byte(strings.Join(checksums, "\n")+"\n"), 0644)
}

// sourceFiles uses an explicit public input set so packaging cannot disclose
// an owner's local caches, metadata, binaries or unrelated root files.
func sourceFiles(root string) ([]archiveEntry, error) {
	topFiles := map[string]bool{}
	for _, name := range []string{"go.mod", "go.sum", "LICENSE", "COPYRIGHT", "README.md", "CONTRIBUTING.md", "CODE_OF_CONDUCT.md", "SECURITY.md", "SUPPORT.md", "THIRD_PARTY_NOTICES.md", "THIRD_PARTY_LICENSES.md", "CHANGELOG.md", "AGENTS.md", ".gitignore", ".editorconfig", ".gitattributes"} {
		topFiles[name] = true
	}
	directories := map[string]bool{"cmd": true, "internal": true, "tools": true, "tests": true, "docs": true, "schema": true, "examples": true, "scripts": true, ".github": true, "prebuilt": true}
	entries := []archiveEntry{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		top := strings.SplitN(relative, "/", 2)[0]
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			if !directories[top] {
				return fs.SkipDir
			}
			return nil
		}
		if !directories[top] && !topFiles[relative] {
			return nil
		}
		if top == "prebuilt" && relative != "prebuilt/README.md" {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("source input must be regular: %s", relative)
		}
		body, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0644)
		if strings.HasSuffix(relative, ".sh") {
			mode = 0755
		}
		entries = append(entries, archiveEntry{Name: relative, Data: body, Mode: mode})
		return nil
	})
	return entries, err
}

func correspondingSource(ctx context.Context, root, version string) ([]archiveEntry, error) {
	entries, err := sourceFiles(root)
	if err != nil {
		return nil, err
	}
	// Vendor only in a private disposable copy, never in the consumer checkout.
	temporary, err := os.MkdirTemp("", "workcopy-source-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	for _, entry := range entries {
		filename := filepath.Join(temporary, filepath.FromSlash(entry.Name))
		if err = os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			return nil, err
		}
		if err = os.WriteFile(filename, entry.Data, entry.Mode); err != nil {
			return nil, err
		}
	}
	process := exec.CommandContext(ctx, "go", "mod", "vendor")
	process.Dir = temporary
	process.Stdout, process.Stderr = os.Stdout, os.Stderr
	if err = process.Run(); err != nil {
		return nil, fmt.Errorf("prepare corresponding dependency source: %w", err)
	}
	err = filepath.WalkDir(filepath.Join(temporary, "vendor"), func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("dependency source must be regular")
		}
		relative, err := filepath.Rel(temporary, name)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		entries = append(entries, archiveEntry{Name: filepath.ToSlash(relative), Data: body, Mode: 0644})
		return nil
	})
	if err != nil {
		return nil, err
	}
	info := "Version: " + version + "\nCompiler: " + runtime.Version() + "\nCompiler source: https://go.dev/dl/" + runtime.Version() + ".src.tar.gz\nSet CGO_ENABLED=0 and your GOOS/GOARCH.\nBuild: go build -mod=vendor -trimpath -buildvcs=false -ldflags=\"-X workcopy/internal/cli.Version=" + version + "\" ./cmd/workcopy\n"
	entries = append(entries, archiveEntry{Name: "BUILDINFO.txt", Data: []byte(info), Mode: 0644})
	return entries, nil
}
func documentation(root string) ([]archiveEntry, error) {
	result := []archiveEntry{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			top := strings.SplitN(relative, "/", 2)[0]
			if relative != "." && top != "docs" && top != "examples" && top != "schema" && top != "prebuilt" {
				return fs.SkipDir
			}
			return nil
		}
		include := relative == "LICENSE" || relative == "COPYRIGHT" || strings.HasSuffix(relative, ".md") || strings.HasPrefix(relative, "examples/") || strings.HasPrefix(relative, "schema/")
		if !include {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("documentation must be regular files")
		}
		body, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		result = append(result, archiveEntry{Name: relative, Data: body, Mode: 0644})
		return nil
	})
	return result, err
}
