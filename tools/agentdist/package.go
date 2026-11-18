package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func targetPackageFiles(repositoryRoot, distributionRoot string, definition definition, item target, version string) (map[string][]byte, error) {
	templateRoot := filepath.Join(distributionRoot, filepath.FromSlash(item.Source))
	files := map[string][]byte{}
	if err := collectPackageFiles(templateRoot, templateRoot, files); err != nil {
		return nil, err
	}
	skillsRoot := filepath.Join(distributionRoot, filepath.FromSlash(definition.SkillsRoot))
	skillFiles := map[string][]byte{}
	if err := collectPackageFiles(skillsRoot, skillsRoot, skillFiles); err != nil {
		return nil, err
	}
	for name, body := range skillFiles {
		destination := strings.TrimSuffix(item.SkillsPath, "/") + "/" + name
		if existing, exists := files[destination]; exists {
			if !bytes.Equal(existing, body) {
				return nil, fmt.Errorf("target %s contains a stale copy of canonical skill %q", item.ID, destination)
			}
			continue
		}
		files[destination] = body
	}
	if _, found := files["README.md"]; !found {
		body, err := regularFile(filepath.Join(distributionRoot, filepath.FromSlash(definition.PluginRoot), "README.md"))
		if err != nil {
			return nil, fmt.Errorf("canonical package README: %w", err)
		}
		files["README.md"] = body
	}
	metadata, err := regularFile(filepath.Join(distributionRoot, "marketplace-metadata.json"))
	if err != nil {
		return nil, fmt.Errorf("marketplace metadata: %w", err)
	}
	files["MARKETPLACE-METADATA.json"] = metadata
	for _, license := range definition.LicenseFiles {
		body, err := regularFile(filepath.Join(repositoryRoot, filepath.FromSlash(license)))
		if err != nil {
			return nil, fmt.Errorf("license file %s: %w", license, err)
		}
		files[license] = body
	}
	for name, body := range files {
		if filepath.Base(name) == "plugin.json" {
			rendered, renderErr := replaceManifestVersion(body, version, definition.DefaultVersion)
			if renderErr != nil {
				return nil, fmt.Errorf("render %s: %w", name, renderErr)
			}
			files[name] = []byte(rendered)
		}
	}
	if err = validateRenderedJSONFiles(files); err != nil {
		return nil, err
	}
	return files, nil
}

func collectPackageFiles(root, current string, files map[string][]byte) error {
	return filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filename == root {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("package input must not be a symlink: %s", filename)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("package input must be a regular file: %s", filename)
		}
		relative, err := filepath.Rel(current, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !localRelativePath(relative) {
			return fmt.Errorf("unsafe package path %q", relative)
		}
		body, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		if _, exists := files[relative]; exists {
			return fmt.Errorf("duplicate package path %q", relative)
		}
		files[relative] = body
		return nil
	})
}

func replaceManifestVersion(body []byte, version, defaultVersion string) (string, error) {
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(body, &manifest); err != nil {
		return "", err
	}
	rawVersion, found := manifest["version"]
	if !found {
		return string(body), nil
	}
	var current string
	if err := json.Unmarshal(rawVersion, &current); err != nil {
		return "", fmt.Errorf("version must be a JSON string")
	}
	if current != defaultVersion && current != "{{VERSION}}" && !validDistributionVersion(current) {
		return "", fmt.Errorf("unexpected manifest version %q", current)
	}
	manifest["version"], _ = json.Marshal(version)
	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	return string(updated) + "\n", nil
}

func packageDistribution(repositoryRoot string, definition definition, version, outputDirectory string) (result error) {
	info, err := os.Lstat(outputDirectory)
	if os.IsNotExist(err) {
		if err = os.MkdirAll(outputDirectory, 0755); err != nil {
			return fmt.Errorf("create package output directory: %w", err)
		}
		info, err = os.Lstat(outputDirectory)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("package output must be a real directory")
	}
	distributionRoot := definition.root
	created := []string{}
	defer func() {
		if result != nil {
			for _, name := range created {
				_ = os.Remove(filepath.Join(outputDirectory, name))
			}
		}
	}()
	checksums := make([]string, 0, len(definition.Targets))
	for _, item := range definition.Targets {
		files, err := targetPackageFiles(repositoryRoot, distributionRoot, definition, item, version)
		if err != nil {
			return fmt.Errorf("assemble %s: %w", item.ID, err)
		}
		name := item.Archive + "_v" + version + ".zip"
		filename := filepath.Join(outputDirectory, name)
		if err = writePackageArchive(filename, files, item.ExecutableFiles); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		created = append(created, name)
		body, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(body)
		checksums = append(checksums, hex.EncodeToString(digest[:])+"  "+name)
	}
	sort.Strings(checksums)
	checksumName := "AGENT_PLUGIN_SHA256SUMS"
	checksumPath := filepath.Join(outputDirectory, checksumName)
	checksumFile, err := os.OpenFile(checksumPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("create package checksums without overwriting: %w", err)
	}
	created = append(created, checksumName)
	if _, err = checksumFile.WriteString(strings.Join(checksums, "\n") + "\n"); err != nil {
		_ = checksumFile.Close()
		return err
	}
	if err = checksumFile.Close(); err != nil {
		return err
	}
	return nil
}

func writePackageArchive(filename string, files map[string][]byte, executableFiles []string) (result error) {
	archive, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	created := true
	defer func() {
		if closeErr := archive.Close(); result == nil && closeErr != nil {
			result = closeErr
		}
		if result != nil && created {
			_ = os.Remove(filename)
		}
	}()
	writer := zip.NewWriter(archive)
	executableSet := make(map[string]bool, len(executableFiles))
	for _, name := range executableFiles {
		if _, found := files[name]; !found {
			_ = writer.Close()
			return fmt.Errorf("configured executable file %q is missing from package inputs", name)
		}
		executableSet[name] = true
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	epoch := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, name := range names {
		if !localRelativePath(name) {
			_ = writer.Close()
			return fmt.Errorf("unsafe archive member %q", name)
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: epoch}
		mode := fs.FileMode(0644)
		if executableSet[name] {
			mode = 0755
		}
		header.SetMode(mode)
		member, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			return err
		}
		if _, err = member.Write(files[name]); err != nil {
			_ = writer.Close()
			return err
		}
	}
	if err = writer.Close(); err != nil {
		return err
	}
	created = true
	return nil
}
