package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const agentPluginManifestSchema = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
const agentPluginMCPManifestSchema = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
var pluginNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
var skillPathReferencePattern = regexp.MustCompile(`(?:\]\()?((?:references|scripts|assets)/[A-Za-z0-9._/-]+)`)

type portablePluginManifest struct {
	Schema      string                     `json:"$schema"`
	Name        string                     `json:"name"`
	Version     string                     `json:"version"`
	Description string                     `json:"description"`
	Author      *pluginAuthor              `json:"author,omitempty"`
	Homepage    string                     `json:"homepage,omitempty"`
	Repository  string                     `json:"repository,omitempty"`
	License     string                     `json:"license,omitempty"`
	Keywords    []string                   `json:"keywords,omitempty"`
	Extensions  map[string]json.RawMessage `json:"extensions,omitempty"`
}

type pluginAuthor struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

type portableMCPConfig struct {
	Schema  string                       `json:"$schema"`
	Servers map[string]portableMCPServer `json:"mcpServers"`
}

type portableMCPServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type claudePluginManifest struct {
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	Description string        `json:"description"`
	Author      *pluginAuthor `json:"author,omitempty"`
	License     string        `json:"license,omitempty"`
}

type antigravityPluginManifest struct {
	Schema           string        `json:"$schema"`
	Name             string        `json:"name"`
	DisplayName      string        `json:"displayName"`
	Version          string        `json:"version"`
	Description      string        `json:"description"`
	Logo             string        `json:"logo,omitempty"`
	SuggestedPrompts []string      `json:"suggestedPrompts"`
	Author           *pluginAuthor `json:"author,omitempty"`
	Homepage         string        `json:"homepage,omitempty"`
	Repository       string        `json:"repository,omitempty"`
	License          string        `json:"license,omitempty"`
	Keywords         []string      `json:"keywords,omitempty"`
}

type cursorPluginManifest struct {
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	Description string        `json:"description"`
	Author      *pluginAuthor `json:"author,omitempty"`
	License     string        `json:"license,omitempty"`
	Keywords    []string      `json:"keywords,omitempty"`
}

type nativeMCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type nativeMCPConfig struct {
	MCPServers map[string]nativeMCPServer `json:"mcpServers"`
}

type vscodeMCPServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type vscodeMCPConfig struct {
	Servers map[string]vscodeMCPServer `json:"servers"`
}

type openCodeMCPServer struct {
	Type     string            `json:"type"`
	Command  []string          `json:"command"`
	Env      map[string]string `json:"environment,omitempty"`
	CodeMode *bool             `json:"codemode,omitempty"`
}

type openCodeMCPConfig struct {
	Schema string `json:"$schema,omitempty"`
	MCP    struct {
		Servers map[string]openCodeMCPServer `json:"servers"`
	} `json:"mcp"`
}

type marketplaceMetadata struct {
	Name               string   `json:"name"`
	PluginID           string   `json:"pluginId"`
	Description        string   `json:"description"`
	Publisher          string   `json:"publisher"`
	Repository         string   `json:"repository"`
	Homepage           string   `json:"homepage"`
	License            string   `json:"license"`
	Categories         []string `json:"categories"`
	Keywords           []string `json:"keywords"`
	SupportedPlatforms []string `json:"supportedPlatforms"`
	Privacy            string   `json:"privacy"`
	SuggestedPrompts   []string `json:"suggestedPrompts"`
	VisualAssets       []string `json:"visualAssets"`
	Screenshots        []string `json:"screenshots"`
}

type githubMarketplaceManifest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Owner       struct {
		Name  string `json:"name"`
		Email string `json:"email,omitempty"`
	} `json:"owner"`
	Metadata struct {
		Description string `json:"description,omitempty"`
		Version     string `json:"version,omitempty"`
		PluginRoot  string `json:"pluginRoot,omitempty"`
	} `json:"metadata,omitempty"`
	Plugins []struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Version     string          `json:"version,omitempty"`
		Source      json.RawMessage `json:"source"`
		Author      *pluginAuthor   `json:"author,omitempty"`
		Homepage    string          `json:"homepage,omitempty"`
		Repository  string          `json:"repository,omitempty"`
		License     string          `json:"license,omitempty"`
		Keywords    []string        `json:"keywords,omitempty"`
		Category    string          `json:"category,omitempty"`
		Tags        []string        `json:"tags,omitempty"`
	} `json:"plugins"`
}

type validatedSkill struct {
	Name string
	Path string
	Body string
}

func validateDistribution(repositoryRoot string) error {
	return validateDistributionAt(repositoryRoot, defaultDefinitionPath)
}

func validateDistributionAt(repositoryRoot, configPath string) error {
	definition, err := loadDefinitionAt(repositoryRoot, configPath)
	if err != nil {
		return err
	}
	distributionRoot := definition.root
	pluginRoot := filepath.Join(distributionRoot, filepath.FromSlash(definition.PluginRoot))
	skillsRoot := filepath.Join(distributionRoot, filepath.FromSlash(definition.SkillsRoot))
	if err = validatePluginManifest(filepath.Join(pluginRoot, "plugin.json"), definition.DefaultVersion, definition.License); err != nil {
		return err
	}
	if err = validatePortableMCP(filepath.Join(pluginRoot, "mcp.json")); err != nil {
		return err
	}
	for _, license := range definition.LicenseFiles {
		if _, err = regularFile(filepath.Join(repositoryRoot, license)); err != nil {
			return fmt.Errorf("required package license %s: %w", license, err)
		}
	}
	metadataPath := filepath.Join(distributionRoot, "marketplace-metadata.json")
	if err = validateMarketplaceMetadata(metadataPath, filepath.Join(distributionRoot, filepath.FromSlash(definition.PluginRoot), "plugin.json")); err != nil {
		return err
	}
	if definition.GitHubMarketplace != "" {
		if err = validateGitHubMarketplace(repositoryRoot, definition); err != nil {
			return err
		}
	}
	if definition.ClaudeMarketplace != "" {
		if err = validateNativeRepositoryMarketplace(repositoryRoot, definition, definition.ClaudeMarketplace, "claude-code", ".claude-plugin/plugin.json", "claude"); err != nil {
			return fmt.Errorf("Claude Code marketplace: %w", err)
		}
	}
	if definition.CursorMarketplace != "" {
		if err = validateNativeRepositoryMarketplace(repositoryRoot, definition, definition.CursorMarketplace, "cursor", ".cursor-plugin/plugin.json", "cursor"); err != nil {
			return fmt.Errorf("Cursor marketplace: %w", err)
		}
	}
	skills, err := readSkills(skillsRoot)
	if err != nil {
		return err
	}
	tools := map[string]map[string]toolField{}
	toolReferencePattern := (*regexp.Regexp)(nil)
	if definition.MCPRegistrationSource != "" {
		tools, err = readMCPToolContractsAt(filepath.Join(repositoryRoot, filepath.FromSlash(definition.MCPRegistrationSource)), definition.ExpectedMCPTools, definition.MCPToolPrefix)
		if err != nil {
			return err
		}
		toolReferencePattern = mcpToolReferencePatternFor(definition.MCPToolPrefix)
		contractPath := filepath.Join(distributionRoot, filepath.FromSlash(definition.ContractExamples))
		if err = validateContractExamples(contractPath, tools); err != nil {
			return err
		}
	}
	commands := map[string]bool{}
	cliReferencePattern := (*regexp.Regexp)(nil)
	if definition.CLIHelpSource != "" {
		commands, err = readCLICommandsAt(filepath.Join(repositoryRoot, filepath.FromSlash(definition.CLIHelpSource)), definition.CLIHelpConstant, definition.CLIName)
		if err != nil {
			return err
		}
		cliReferencePattern = cliCommandReferencePatternFor(definition.CLIName)
	}
	for _, skill := range skills {
		if toolReferencePattern != nil {
			for _, name := range toolReferencePattern.FindAllString(skill.Body, -1) {
				if _, found := tools[name]; !found {
					return fmt.Errorf("skill %s references unregistered MCP tool %q", skill.Name, name)
				}
			}
		}
		if cliReferencePattern != nil {
			for _, match := range cliReferencePattern.FindAllStringSubmatch(skill.Body, -1) {
				if len(match) != 2 || !commands[match[1]] {
					return fmt.Errorf("skill %s references a CLI command that is not declared in its distribution definition: %q", skill.Name, match[1])
				}
			}
		}
	}
	for _, item := range definition.Targets {
		if item.PublishSource {
			if err = validatePublishedSourceFiles(repositoryRoot, definition, item); err != nil {
				return fmt.Errorf("publication source %s: %w", item.ID, err)
			}
			if err = validatePublishedSkillTree(distributionRoot, definition, item); err != nil {
				return fmt.Errorf("publication source %s: %w", item.ID, err)
			}
		}
		if _, err = targetPackageFiles(repositoryRoot, distributionRoot, definition, item, definition.DefaultVersion); err != nil {
			return fmt.Errorf("adapter %s: %w", item.ID, err)
		}
		if err = validateRenderedTarget(repositoryRoot, distributionRoot, definition, item); err != nil {
			return fmt.Errorf("adapter %s: %w", item.ID, err)
		}
	}
	return nil
}

func validateMarketplaceMetadata(filename, manifestFilename string) error {
	body, err := regularFile(filename)
	if err != nil {
		return fmt.Errorf("marketplace metadata: %w", err)
	}
	var metadata marketplaceMetadata
	if err = decodeStrict(body, &metadata); err != nil {
		return fmt.Errorf("invalid marketplace metadata: %w", err)
	}
	if strings.TrimSpace(metadata.Name) == "" || !validAgentPluginName(metadata.PluginID) || strings.TrimSpace(metadata.Description) == "" || strings.TrimSpace(metadata.Publisher) == "" || strings.TrimSpace(metadata.Repository) == "" || strings.TrimSpace(metadata.Homepage) == "" || metadata.License == "" || len(metadata.Categories) == 0 || len(metadata.Keywords) == 0 || len(metadata.SupportedPlatforms) == 0 || strings.TrimSpace(metadata.Privacy) == "" || len(metadata.SuggestedPrompts) == 0 {
		return fmt.Errorf("marketplace metadata requires truthful identity, capability, platform, privacy, keyword, category, and prompt fields")
	}
	manifestBody, err := regularFile(manifestFilename)
	if err != nil {
		return err
	}
	var manifest portablePluginManifest
	if err = decodeStrict(manifestBody, &manifest); err != nil {
		return fmt.Errorf("portable plugin manifest: %w", err)
	}
	if metadata.PluginID != manifest.Name || metadata.License != manifest.License {
		return fmt.Errorf("marketplace plugin ID and license must match the portable manifest")
	}
	for _, paths := range [][]string{metadata.VisualAssets, metadata.Screenshots} {
		for _, path := range paths {
			if !localRelativePath(path) {
				return fmt.Errorf("marketplace asset path %q must remain inside the product distribution", path)
			}
			if _, err = regularFile(filepath.Join(filepath.Dir(filename), filepath.FromSlash(path))); err != nil {
				return fmt.Errorf("marketplace asset %s: %w", path, err)
			}
		}
	}
	return nil
}

func validateGitHubMarketplace(repositoryRoot string, definition definition) error {
	filename := filepath.Join(repositoryRoot, filepath.FromSlash(definition.GitHubMarketplace))
	body, err := regularFile(filename)
	if err != nil {
		return fmt.Errorf("GitHub plugin marketplace: %w", err)
	}
	var marketplace githubMarketplaceManifest
	if err = decodeStrict(body, &marketplace); err != nil {
		return fmt.Errorf("invalid GitHub plugin marketplace: %w", err)
	}
	if !skillNamePattern.MatchString(marketplace.Name) || strings.TrimSpace(marketplace.Owner.Name) == "" || len(marketplace.Plugins) == 0 {
		return fmt.Errorf("GitHub marketplace must declare a stable name, owner, and plugin list")
	}
	manifestBody, err := regularFile(filepath.Join(definition.root, filepath.FromSlash(definition.PluginRoot), "plugin.json"))
	if err != nil {
		return err
	}
	var manifest portablePluginManifest
	if err = decodeStrict(manifestBody, &manifest); err != nil {
		return fmt.Errorf("portable plugin manifest: %w", err)
	}
	pluginPath, err := filepath.Rel(repositoryRoot, filepath.Join(definition.root, filepath.FromSlash(definition.PluginRoot)))
	if err != nil {
		return err
	}
	return validateMarketplaceListing(filename, marketplace, manifest.Name, definition.DefaultVersion, filepath.ToSlash(pluginPath))
}

func validateNativeRepositoryMarketplace(repositoryRoot string, definition definition, marketplacePath, targetID, manifestPath, manifestKind string) error {
	var item *target
	for index := range definition.Targets {
		if definition.Targets[index].ID == targetID {
			item = &definition.Targets[index]
			break
		}
	}
	if item == nil {
		return fmt.Errorf("distribution has no %s adapter", targetID)
	}
	if !item.PublishSource {
		return fmt.Errorf("adapter %s is not declared as a repository publication source", targetID)
	}
	if err := validatePublishedSkillTree(definition.root, definition, *item); err != nil {
		return err
	}
	if err := validatePublishedSourceFiles(repositoryRoot, definition, *item); err != nil {
		return err
	}
	manifestFilename := filepath.Join(definition.root, filepath.FromSlash(item.Source), filepath.FromSlash(manifestPath))
	manifestBody, err := regularFile(manifestFilename)
	if err != nil {
		return fmt.Errorf("adapter manifest: %w", err)
	}
	if err = validateNativePluginManifest(map[string][]byte{manifestPath: manifestBody}, manifestPath, manifestKind, definition.DefaultVersion, definition.License); err != nil {
		return err
	}
	var manifest repositoryPluginManifest
	if err = decodeStrict(manifestBody, &manifest); err != nil {
		return fmt.Errorf("invalid adapter manifest: %w", err)
	}
	pluginPath, err := filepath.Rel(repositoryRoot, filepath.Join(definition.root, filepath.FromSlash(item.Source)))
	if err != nil {
		return err
	}
	filename := filepath.Join(repositoryRoot, filepath.FromSlash(marketplacePath))
	body, err := regularFile(filename)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var marketplace githubMarketplaceManifest
	if err = decodeStrict(body, &marketplace); err != nil {
		return fmt.Errorf("invalid manifest: %w", err)
	}
	if !skillNamePattern.MatchString(marketplace.Name) || strings.TrimSpace(marketplace.Owner.Name) == "" || len(marketplace.Plugins) == 0 {
		return fmt.Errorf("marketplace must declare a stable name, owner, and plugin list")
	}
	return validateMarketplaceListing(filename, marketplace, manifest.Name, definition.DefaultVersion, filepath.ToSlash(pluginPath))
}

type repositoryPluginManifest struct {
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	Description string        `json:"description"`
	Author      *pluginAuthor `json:"author,omitempty"`
	Homepage    string        `json:"homepage,omitempty"`
	Repository  string        `json:"repository,omitempty"`
	License     string        `json:"license,omitempty"`
	Keywords    []string      `json:"keywords,omitempty"`
}

func validateMarketplaceListing(filename string, marketplace githubMarketplaceManifest, expectedName, expectedVersion, expectedSource string) error {
	seen := map[string]bool{}
	found := false
	for _, plugin := range marketplace.Plugins {
		if plugin.Name == "" || seen[plugin.Name] {
			return fmt.Errorf("marketplace has an invalid or duplicate plugin name %q", plugin.Name)
		}
		seen[plugin.Name] = true
		if plugin.Name != expectedName {
			continue
		}
		var source string
		if err := json.Unmarshal(plugin.Source, &source); err != nil {
			return fmt.Errorf("marketplace plugin %s must use a relative source directory", plugin.Name)
		}
		normalizedSource, valid := normalizeMarketplaceSource(source)
		if !valid || normalizedSource != expectedSource {
			return fmt.Errorf("marketplace plugin %s source %q must point to %s", plugin.Name, source, expectedSource)
		}
		if strings.TrimSpace(plugin.Description) == "" || plugin.Version != "" && plugin.Version != expectedVersion {
			return fmt.Errorf("marketplace plugin %s must have a description and a version matching the product definition when supplied", plugin.Name)
		}
		found = true
	}
	if !found {
		return fmt.Errorf("%s does not list plugin %s", filepath.Base(filename), expectedName)
	}
	return nil
}

func normalizeMarketplaceSource(value string) (string, bool) {
	value = strings.TrimPrefix(value, "./")
	if !localRelativePath(value) {
		return "", false
	}
	return value, true
}

func validatePluginManifest(filename, expectedVersion, expectedLicense string) error {
	body, err := regularFile(filename)
	if err != nil {
		return fmt.Errorf("plugin manifest: %w", err)
	}
	var manifest portablePluginManifest
	if err = decodeStrict(body, &manifest); err != nil {
		return fmt.Errorf("invalid plugin manifest: %w", err)
	}
	if manifest.Schema != agentPluginManifestSchema || !validAgentPluginName(manifest.Name) || manifest.Version != expectedVersion || strings.TrimSpace(manifest.Description) == "" || manifest.License != expectedLicense {
		return fmt.Errorf("plugin manifest must target Agent Plugins 1.0, have a valid name/version/description, and match the distribution license %q", expectedLicense)
	}
	return nil
}

func validAgentPluginName(value string) bool {
	return len(value) >= 1 && len(value) <= 64 && pluginNamePattern.MatchString(value) && !strings.Contains(value, "--") && !strings.Contains(value, "..")
}

func validatePortableMCP(filename string) error {
	body, err := regularFile(filename)
	if err != nil {
		return fmt.Errorf("MCP configuration: %w", err)
	}
	return validatePortableMCPBytes(body)
}

func validatePortableMCPBytes(body []byte) error {
	var config portableMCPConfig
	if err := decodeStrict(body, &config); err != nil {
		return fmt.Errorf("invalid MCP configuration: %w", err)
	}
	if config.Schema != agentPluginMCPManifestSchema || len(config.Servers) == 0 {
		return fmt.Errorf("MCP configuration must target Agent Plugins 1.0 and declare at least one server")
	}
	for name, server := range config.Servers {
		if !targetIDPattern.MatchString(name) {
			return fmt.Errorf("MCP server name %q is invalid", name)
		}
		if server.Type != "stdio" || !validPluginCommand(server.Command) || server.URL != "" || server.Headers != nil {
			return fmt.Errorf("MCP server %q must use a valid stdio command", name)
		}
		for key, value := range server.Env {
			if strings.EqualFold(key, "PLUGIN_ROOT") || strings.EqualFold(key, "PLUGIN_DATA") {
				return fmt.Errorf("MCP server %q must not set reserved environment variable %s", name, key)
			}
			if err := validatePluginPlaceholders(value); err != nil {
				return fmt.Errorf("MCP server %q env %s: %w", name, key, err)
			}
		}
		for _, argument := range server.Args {
			if err := validatePluginPlaceholders(argument); err != nil {
				return fmt.Errorf("MCP server %q argument: %w", name, err)
			}
		}
		if err := validatePluginPlaceholders(server.CWD); err != nil {
			return fmt.Errorf("MCP server %q cwd: %w", name, err)
		}
		if server.CWD != "" && !validPluginCWD(server.CWD) {
			return fmt.Errorf("MCP server %q has an unsupported cwd path", name)
		}
	}
	return nil
}

func validPluginCommand(command string) bool {
	if command == "" || strings.ContainsAny(command, " \t\r\n") || strings.Contains(command, "${") {
		return false
	}
	if strings.HasPrefix(command, "./") {
		return localRelativePath(command[2:])
	}
	return !strings.ContainsAny(command, "/\\:") && command != "." && command != ".."
}

func validatePluginPlaceholders(value string) error {
	for remaining := value; ; {
		index := strings.Index(remaining, "${")
		if index < 0 {
			return nil
		}
		remaining = remaining[index:]
		if !strings.HasPrefix(remaining, "${PLUGIN_ROOT}") && !strings.HasPrefix(remaining, "${PLUGIN_DATA}") {
			return fmt.Errorf("unsupported placeholder in %q", value)
		}
		if strings.HasPrefix(remaining, "${PLUGIN_ROOT}") {
			remaining = remaining[len("${PLUGIN_ROOT}"):]
		} else {
			remaining = remaining[len("${PLUGIN_DATA}"):]
		}
	}
}

func validPluginCWD(value string) bool {
	if strings.HasPrefix(value, "./") {
		return localRelativePath(value[2:])
	}
	for _, root := range []string{"${PLUGIN_ROOT}", "${PLUGIN_DATA}"} {
		if value == root {
			return true
		}
		if strings.HasPrefix(value, root+"/") {
			return localRelativePath(strings.TrimPrefix(value, root+"/"))
		}
	}
	return false
}

func readSkills(root string) ([]validatedSkill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read canonical skills: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("canonical skills directory is empty")
	}
	seen := map[string]bool{}
	result := make([]validatedSkill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("skill entry %q must be a regular directory", entry.Name())
		}
		if !skillNamePattern.MatchString(entry.Name()) || seen[entry.Name()] {
			return nil, fmt.Errorf("invalid or duplicate skill directory %q", entry.Name())
		}
		folder := filepath.Join(root, entry.Name())
		filename := filepath.Join(folder, "SKILL.md")
		body, err := regularFile(filename)
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", entry.Name(), err)
		}
		frontmatter, markdown, err := parseSkillFrontmatter(string(body))
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", entry.Name(), err)
		}
		if frontmatter["name"] != entry.Name() || !skillNamePattern.MatchString(frontmatter["name"]) {
			return nil, fmt.Errorf("skill %s name must be valid and match its directory", entry.Name())
		}
		description := frontmatter["description"]
		if strings.TrimSpace(description) == "" || utf8.RuneCountInString(description) > 1024 {
			return nil, fmt.Errorf("skill %s description must contain 1..1024 characters", entry.Name())
		}
		if compatibility := frontmatter["compatibility"]; compatibility != "" && utf8.RuneCountInString(compatibility) > 500 {
			return nil, fmt.Errorf("skill %s compatibility exceeds 500 characters", entry.Name())
		}
		if lineCount := bytes.Count([]byte(markdown), []byte("\n")) + 1; lineCount > 500 {
			return nil, fmt.Errorf("skill %s is %d lines; keep the main instructions under 500", entry.Name(), lineCount)
		}
		for _, match := range skillPathReferencePattern.FindAllStringSubmatch(markdown, -1) {
			if len(match) != 2 || !localRelativePath(match[1]) {
				return nil, fmt.Errorf("skill %s contains an unsafe resource path", entry.Name())
			}
			if _, err = regularFile(filepath.Join(folder, filepath.FromSlash(match[1]))); err != nil {
				return nil, fmt.Errorf("skill %s references missing resource %s", entry.Name(), match[1])
			}
		}
		seen[entry.Name()] = true
		result = append(result, validatedSkill{Name: entry.Name(), Path: filename, Body: markdown})
	}
	return result, nil
}

func parseSkillFrontmatter(body string) (map[string]string, string, error) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	if len(lines) < 4 || lines[0] != "---" {
		return nil, "", fmt.Errorf("SKILL.md must start with YAML frontmatter")
	}
	end := -1
	fields := map[string]string{}
	allowed := map[string]bool{"name": true, "description": true, "license": true, "compatibility": true, "allowed-tools": true}
	for index := 1; index < len(lines); index++ {
		if lines[index] == "---" {
			end = index
			break
		}
		parts := strings.SplitN(lines[index], ":", 2)
		if len(parts) != 2 {
			return nil, "", fmt.Errorf("invalid flat YAML frontmatter line %q", lines[index])
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if !allowed[key] || value == "" {
			return nil, "", fmt.Errorf("unsupported or empty frontmatter field %q", key)
		}
		if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) || (strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
			value = value[1 : len(value)-1]
		}
		if fields[key] != "" {
			return nil, "", fmt.Errorf("duplicate frontmatter field %q", key)
		}
		fields[key] = value
	}
	if end < 0 || fields["name"] == "" || fields["description"] == "" {
		return nil, "", fmt.Errorf("frontmatter requires name and description")
	}
	return fields, strings.Join(lines[end+1:], "\n"), nil
}

func validateRenderedTarget(repositoryRoot, distributionRoot string, definition definition, item target) error {
	files, err := targetPackageFiles(repositoryRoot, distributionRoot, definition, item, definition.DefaultVersion)
	if err != nil {
		return err
	}
	for name, body := range files {
		if strings.HasSuffix(name, ".json") {
			if !json.Valid(body) {
				return fmt.Errorf("invalid JSON in %s", name)
			}
		}
	}
	if item.ID == "plugin" || item.ID == "openai-skills-only" || item.ID == "kiro-power" {
		body, found := files["plugin.json"]
		if !found {
			return fmt.Errorf("portable package is missing root plugin.json")
		}
		var manifest portablePluginManifest
		if err = decodeStrict(body, &manifest); err != nil {
			return fmt.Errorf("portable manifest: %w", err)
		}
		if manifest.Schema != agentPluginManifestSchema || manifest.Version != definition.DefaultVersion {
			return fmt.Errorf("portable manifest schema or version mismatch")
		}
	}
	if item.ID == "plugin" || item.ID == "cursor" || item.ID == "kiro-power" {
		if _, found := files["mcp.json"]; !found {
			return fmt.Errorf("portable MCP package is missing mcp.json")
		}
		if err = validatePortableMCPBytes(files["mcp.json"]); err != nil {
			return err
		}
	}
	switch item.ID {
	case "claude-code":
		if err = validateNativePluginManifest(files, ".claude-plugin/plugin.json", "claude", definition.DefaultVersion, definition.License); err != nil {
			return err
		}
		if err = validateNativeMCPConfig(files, ".mcp.json"); err != nil {
			return err
		}
	case "antigravity":
		if err = validateAntigravityManifest(files, definition.DefaultVersion, definition.License); err != nil {
			return err
		}
		if err = validateNativeMCPConfig(files, "mcp_config.json"); err != nil {
			return err
		}
	case "cursor":
		if err = validateNativePluginManifest(files, ".cursor-plugin/plugin.json", "cursor", definition.DefaultVersion, definition.License); err != nil {
			return err
		}
	case "opencode":
		if err = validateOpenCodeMCPConfig(files, "opencode.json"); err != nil {
			return err
		}
	case "kiro":
		if err = validateNativeMCPConfig(files, "mcp.json"); err != nil {
			return err
		}
	case "vscode-copilot":
		if err = validateVSCodeMCPConfig(files, ".vscode/mcp.json"); err != nil {
			return err
		}
	}
	return nil
}

func validateNativePluginManifest(files map[string][]byte, name, kind, expectedVersion, expectedLicense string) error {
	body, found := files[name]
	if !found {
		return fmt.Errorf("native %s manifest is missing %s", kind, name)
	}
	switch kind {
	case "claude":
		var manifest claudePluginManifest
		if err := decodeStrict(body, &manifest); err != nil {
			return fmt.Errorf("invalid Claude Code plugin manifest: %w", err)
		}
		if !skillNamePattern.MatchString(manifest.Name) || manifest.Version != expectedVersion || strings.TrimSpace(manifest.Description) == "" || manifest.License != expectedLicense {
			return fmt.Errorf("Claude Code plugin manifest must have a kebab-case name, matching version, description, and distribution license")
		}
	case "cursor":
		var manifest cursorPluginManifest
		if err := decodeStrict(body, &manifest); err != nil {
			return fmt.Errorf("invalid Cursor plugin manifest: %w", err)
		}
		if !skillNamePattern.MatchString(manifest.Name) || manifest.Version != expectedVersion || strings.TrimSpace(manifest.Description) == "" || manifest.License != expectedLicense {
			return fmt.Errorf("Cursor plugin manifest must have a kebab-case name, matching version, description, and distribution license")
		}
	default:
		return fmt.Errorf("unsupported native manifest kind %q", kind)
	}
	return nil
}

func validateAntigravityManifest(files map[string][]byte, expectedVersion, expectedLicense string) error {
	body, found := files[".antigravity-plugin/plugin.json"]
	if !found {
		return fmt.Errorf("Antigravity manifest is missing .antigravity-plugin/plugin.json")
	}
	var manifest antigravityPluginManifest
	if err := decodeStrict(body, &manifest); err != nil {
		return fmt.Errorf("invalid Antigravity plugin manifest: %w", err)
	}
	if manifest.Schema != "https://antigravity.google/schemas/v1/plugin.json" || !skillNamePattern.MatchString(manifest.Name) || strings.TrimSpace(manifest.DisplayName) == "" || manifest.Version != expectedVersion || strings.TrimSpace(manifest.Description) == "" || manifest.Author == nil || strings.TrimSpace(manifest.Author.Name) == "" || manifest.License != expectedLicense || len(manifest.SuggestedPrompts) == 0 || len(manifest.SuggestedPrompts) > 3 {
		return fmt.Errorf("Antigravity manifest must include its schema, name, displayName, matching version and license, description, author, and 1..3 suggestedPrompts")
	}
	for _, prompt := range manifest.SuggestedPrompts {
		if strings.TrimSpace(prompt) == "" {
			return fmt.Errorf("Antigravity suggestedPrompts must not contain empty entries")
		}
	}
	if manifest.Logo != "" {
		if !localRelativePath(manifest.Logo) {
			return fmt.Errorf("Antigravity logo path must stay inside the plugin package")
		}
		if _, found = files[manifest.Logo]; !found {
			return fmt.Errorf("Antigravity logo asset %s is missing", manifest.Logo)
		}
	}
	return nil
}

func validateNativeMCPConfig(files map[string][]byte, name string) error {
	body, found := files[name]
	if !found {
		return fmt.Errorf("native MCP configuration is missing %s", name)
	}
	var config nativeMCPConfig
	if err := decodeStrict(body, &config); err != nil {
		return fmt.Errorf("invalid MCP configuration %s: %w", name, err)
	}
	if len(config.MCPServers) == 0 {
		return fmt.Errorf("MCP configuration %s must declare at least one server", name)
	}
	for serverName, server := range config.MCPServers {
		if !skillNamePattern.MatchString(serverName) || server.Command == "" || strings.ContainsAny(server.Command, " \t\r\n") {
			return fmt.Errorf("MCP configuration %s has invalid server %q", name, serverName)
		}
	}
	return nil
}

func validateVSCodeMCPConfig(files map[string][]byte, name string) error {
	body, found := files[name]
	if !found {
		return fmt.Errorf("VS Code MCP configuration is missing %s", name)
	}
	var config vscodeMCPConfig
	if err := decodeStrict(body, &config); err != nil {
		return fmt.Errorf("invalid VS Code MCP configuration: %w", err)
	}
	if len(config.Servers) == 0 {
		return fmt.Errorf("VS Code MCP configuration must declare at least one server")
	}
	for serverName, server := range config.Servers {
		if !skillNamePattern.MatchString(serverName) || server.Type != "stdio" || server.Command == "" || strings.ContainsAny(server.Command, " \t\r\n") {
			return fmt.Errorf("VS Code MCP configuration has invalid stdio server %q", serverName)
		}
	}
	return nil
}

func validateOpenCodeMCPConfig(files map[string][]byte, name string) error {
	body, found := files[name]
	if !found {
		return fmt.Errorf("OpenCode MCP configuration is missing %s", name)
	}
	var config openCodeMCPConfig
	if err := decodeStrict(body, &config); err != nil {
		return fmt.Errorf("invalid OpenCode MCP configuration: %w", err)
	}
	if len(config.MCP.Servers) == 0 {
		return fmt.Errorf("OpenCode MCP configuration must declare mcp.servers")
	}
	for serverName, server := range config.MCP.Servers {
		if !skillNamePattern.MatchString(serverName) || server.Type != "local" || len(server.Command) == 0 || strings.ContainsAny(server.Command[0], " \t\r\n") {
			return fmt.Errorf("OpenCode MCP configuration has invalid local server %q", serverName)
		}
	}
	return nil
}

func validateJSON(filename string) error {
	body, err := regularFile(filename)
	if err != nil {
		return err
	}
	if !json.Valid(body) {
		return fmt.Errorf("invalid JSON: %s", filename)
	}
	return nil
}

func regularFile(filename string) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must be a regular file", filename)
	}
	return os.ReadFile(filename)
}

func validateRenderedJSONFiles(files map[string][]byte) error {
	for name, body := range files {
		if strings.HasSuffix(name, ".json") && !json.Valid(body) {
			return fmt.Errorf("invalid JSON in %s", name)
		}
	}
	return nil
}

func equalJSON(left, right any) bool {
	leftBody, leftErr := json.Marshal(left)
	rightBody, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBody, rightBody)
}
