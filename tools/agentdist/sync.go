package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type generatedFile struct {
	path     string
	relative string
	body     []byte
	replace  bool
}

func syncPublishedSkills(repositoryRoot string, definition definition, overwrite bool) error {
	distributionRoot := definition.root
	distributionRelative, err := filepath.Rel(repositoryRoot, distributionRoot)
	if err != nil {
		return err
	}
	distributionRelative = filepath.ToSlash(distributionRelative)
	if distributionRelative == "." {
		distributionRelative = ""
	}
	canonicalRoot := filepath.Join(distributionRoot, filepath.FromSlash(definition.SkillsRoot))
	canonical := map[string][]byte{}
	if err := collectPackageFiles(canonicalRoot, canonicalRoot, canonical); err != nil {
		return fmt.Errorf("read canonical skills: %w", err)
	}
	if len(canonical) == 0 {
		return fmt.Errorf("canonical skills directory is empty")
	}
	operations := []generatedFile{}
	sourceCount := 0
	for _, item := range definition.Targets {
		if !item.PublishSource {
			continue
		}
		sourceCount++
		targetRoot := filepath.Join(distributionRoot, filepath.FromSlash(item.Source))
		if err := requireRealDirectory(targetRoot); err != nil {
			return fmt.Errorf("marketplace source %s: %w", item.ID, err)
		}
		publishedRoot := filepath.Join(targetRoot, filepath.FromSlash(item.SkillsPath))
		actual := map[string][]byte{}
		publishedRelative := filepath.ToSlash(filepath.Join(filepath.FromSlash(distributionRelative), filepath.FromSlash(item.Source), filepath.FromSlash(item.SkillsPath)))
		if err := requireExistingPathHasNoSymlinks(repositoryRoot, publishedRelative); err != nil {
			return fmt.Errorf("marketplace source %s: %w", item.ID, err)
		}
		if info, err := os.Lstat(publishedRoot); err == nil {
			if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
				return fmt.Errorf("marketplace source %s skills path must be a real directory", item.ID)
			}
			if err = collectPackageFiles(publishedRoot, publishedRoot, actual); err != nil {
				return fmt.Errorf("marketplace source %s: %w", item.ID, err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("marketplace source %s: %w", item.ID, err)
		}
		expectedNames := make(map[string]bool, len(canonical))
		for name, body := range canonical {
			destination := filepath.ToSlash(filepath.Join(item.SkillsPath, filepath.FromSlash(name)))
			expectedNames[filepath.ToSlash(filepath.FromSlash(name))] = true
			path := filepath.Join(targetRoot, filepath.FromSlash(destination))
			destinationRelative := filepath.ToSlash(filepath.Join(filepath.FromSlash(distributionRelative), filepath.FromSlash(item.Source), filepath.FromSlash(destination)))
			if err := requireExistingPathHasNoSymlinks(repositoryRoot, destinationRelative); err != nil {
				return fmt.Errorf("marketplace source %s: %w", item.ID, err)
			}
			old, exists := actual[filepath.ToSlash(filepath.FromSlash(name))]
			if exists && bytes.Equal(old, body) {
				continue
			}
			if exists && !overwrite {
				return fmt.Errorf("marketplace source %s skill %s is stale; rerun with --overwrite to refresh generated copies", item.ID, name)
			}
			operations = append(operations, generatedFile{
				path: path, relative: destinationRelative, body: body, replace: exists,
			})
		}
		for name := range actual {
			if !expectedNames[name] {
				return fmt.Errorf("marketplace source %s contains stale generated skill file %s; review and remove it before syncing", item.ID, name)
			}
		}
		for _, license := range definition.LicenseFiles {
			body, err := regularFile(filepath.Join(repositoryRoot, filepath.FromSlash(license)))
			if err != nil {
				return fmt.Errorf("read canonical notice %s: %w", license, err)
			}
			destination := filepath.Join(targetRoot, filepath.FromSlash(license))
			destinationRelative := filepath.ToSlash(filepath.Join(filepath.FromSlash(distributionRelative), filepath.FromSlash(item.Source), filepath.FromSlash(license)))
			if err = requireExistingPathHasNoSymlinks(repositoryRoot, destinationRelative); err != nil {
				return fmt.Errorf("marketplace source %s: %w", item.ID, err)
			}
			old, readErr := regularFile(destination)
			exists := readErr == nil
			if readErr != nil && !os.IsNotExist(readErr) {
				return fmt.Errorf("marketplace source %s notice %s: %w", item.ID, license, readErr)
			}
			if exists && bytes.Equal(old, body) {
				continue
			}
			if exists && !overwrite {
				return fmt.Errorf("marketplace source %s notice %s is stale; rerun with --overwrite to refresh generated copies", item.ID, license)
			}
			operations = append(operations, generatedFile{
				path: destination, relative: destinationRelative, body: body, replace: exists,
			})
		}
	}
	if sourceCount == 0 {
		return fmt.Errorf("distribution declares no publishSource targets")
	}
	sort.Slice(operations, func(left, right int) bool { return operations[left].relative < operations[right].relative })
	for _, operation := range operations {
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(operation.relative)))
		if err := ensureRealDirectoryPath(repositoryRoot, parent); err != nil {
			return fmt.Errorf("prepare generated publication path %s: %w", operation.relative, err)
		}
		if err := writeGeneratedFile(operation.path, operation.body, operation.replace); err != nil {
			return fmt.Errorf("write generated publication file %s: %w", operation.relative, err)
		}
	}
	return nil
}

func validatePublishedSkillTree(distributionRoot string, definition definition, item target) error {
	canonicalRoot := filepath.Join(distributionRoot, filepath.FromSlash(definition.SkillsRoot))
	canonical := map[string][]byte{}
	if err := collectPackageFiles(canonicalRoot, canonicalRoot, canonical); err != nil {
		return fmt.Errorf("read canonical skills: %w", err)
	}
	targetRoot := filepath.Join(distributionRoot, filepath.FromSlash(item.Source), filepath.FromSlash(item.SkillsPath))
	if err := requireRealDirectory(targetRoot); err != nil {
		return fmt.Errorf("published skills directory: %w", err)
	}
	actual := map[string][]byte{}
	if err := collectPackageFiles(targetRoot, targetRoot, actual); err != nil {
		return fmt.Errorf("read published skills: %w", err)
	}
	if len(actual) != len(canonical) {
		return fmt.Errorf("published skills tree has %d files; canonical tree has %d", len(actual), len(canonical))
	}
	for name, body := range canonical {
		published, found := actual[name]
		if !found || !bytes.Equal(published, body) {
			return fmt.Errorf("published skill file %s is missing or stale", name)
		}
	}
	return nil
}

func validatePublishedSourceFiles(repositoryRoot string, definition definition, item target) error {
	targetRoot := filepath.Join(definition.root, filepath.FromSlash(item.Source))
	targetRelative, err := filepath.Rel(repositoryRoot, targetRoot)
	if err != nil {
		return err
	}
	targetRelative = filepath.ToSlash(targetRelative)
	if err = requireExistingPathHasNoSymlinks(repositoryRoot, targetRelative); err != nil {
		return fmt.Errorf("published source path: %w", err)
	}
	if err = requireRealDirectory(targetRoot); err != nil {
		return fmt.Errorf("published source: %w", err)
	}
	readme, err := regularFile(filepath.Join(targetRoot, "README.md"))
	if err != nil {
		return fmt.Errorf("published source requires README.md: %w", err)
	}
	if len(bytes.TrimSpace(readme)) == 0 {
		return fmt.Errorf("published source README.md must not be empty")
	}
	for _, license := range definition.LicenseFiles {
		relative := filepath.ToSlash(filepath.Join(filepath.FromSlash(targetRelative), filepath.FromSlash(license)))
		if err = requireExistingPathHasNoSymlinks(repositoryRoot, relative); err != nil {
			return fmt.Errorf("published source notice %s: %w", license, err)
		}
		canonical, readErr := regularFile(filepath.Join(repositoryRoot, filepath.FromSlash(license)))
		if readErr != nil {
			return fmt.Errorf("canonical notice %s: %w", license, readErr)
		}
		published, readErr := regularFile(filepath.Join(targetRoot, filepath.FromSlash(license)))
		if readErr != nil {
			return fmt.Errorf("published source requires notice %s: %w", license, readErr)
		}
		if !bytes.Equal(published, canonical) {
			return fmt.Errorf("published source notice %s is stale", license)
		}
	}
	return nil
}

func requireRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a real directory", path)
	}
	return nil
}

func requireExistingPathHasNoSymlinks(root, relative string) error {
	if !localRelativePath(relative) {
		return fmt.Errorf("path %q is not repository-relative", relative)
	}
	current := root
	segments := splitPortablePath(relative)
	for index, segment := range segments {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("path component %s is a symlink", current)
		}
		if index < len(segments)-1 && !info.IsDir() {
			return fmt.Errorf("path component %s is not a directory", current)
		}
	}
	return nil
}

func splitPortablePath(value string) []string {
	return strings.Split(value, "/")
}

func ensureRealDirectoryPath(root, relative string) error {
	if relative == "." || relative == "" {
		return nil
	}
	if !localRelativePath(relative) {
		return fmt.Errorf("path %q is not repository-relative", relative)
	}
	current := root
	for _, segment := range splitPortablePath(relative) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err = os.Mkdir(current, 0755); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("path component %s is not a real directory", current)
		}
	}
	return nil
}

func writeGeneratedFile(path string, body []byte, replace bool) error {
	if runtime.GOOS == "windows" {
		return writeGeneratedFileWindows(path, body, replace)
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".agentdist-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err = temporary.Write(body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err = temporary.Chmod(0644); err != nil {
		_ = temporary.Close()
		return err
	}
	if err = temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	info, statErr := os.Lstat(path)
	if os.IsNotExist(statErr) {
		if replace {
			return fmt.Errorf("expected existing generated file is missing")
		}
	} else if statErr != nil {
		return statErr
	} else if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("existing generated file must be a regular file")
	} else if !replace {
		return fmt.Errorf("generated file appeared after preflight")
	}
	return os.Rename(temporaryName, path)
}

func writeGeneratedFileWindows(path string, body []byte, replace bool) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if replace {
			return fmt.Errorf("expected existing generated file is missing")
		}
	} else if err != nil {
		return err
	} else if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("existing generated file must be a regular file")
	} else if !replace {
		return fmt.Errorf("generated file appeared after preflight")
	}
	if replace {
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if err = file.Chmod(0644); err != nil {
		return err
	}
	written, writeErr := file.Write(body)
	if writeErr != nil {
		return writeErr
	}
	if written != len(body) {
		return fmt.Errorf("short write: %d of %d bytes", written, len(body))
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	success = true
	return nil
}
