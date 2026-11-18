package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const defaultDefinitionPath = "agent-distribution/workcopy/agentdist.json"

var distributionVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$`)
var targetIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
var mcpToolPrefixPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*_$`)
var mcpToolSuffixPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var cliNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
var cliHelpConstantPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type target struct {
	ID              string   `json:"id"`
	Source          string   `json:"source"`
	SkillsPath      string   `json:"skillsPath"`
	Archive         string   `json:"archive"`
	PublishSource   bool     `json:"publishSource,omitempty"`
	ExecutableFiles []string `json:"executableFiles,omitempty"`
}

type definition struct {
	SchemaVersion         int      `json:"schemaVersion"`
	DefaultVersion        string   `json:"defaultVersion"`
	License               string   `json:"license"`
	PluginRoot            string   `json:"pluginRoot"`
	SkillsRoot            string   `json:"skillsRoot"`
	ContractExamples      string   `json:"contractExamples,omitempty"`
	MCPRegistrationSource string   `json:"mcpRegistrationSource,omitempty"`
	MCPToolPrefix         string   `json:"mcpToolPrefix,omitempty"`
	ExpectedMCPTools      int      `json:"expectedMcpTools,omitempty"`
	CLIHelpSource         string   `json:"cliHelpSource,omitempty"`
	CLIHelpConstant       string   `json:"cliHelpConstant,omitempty"`
	CLIName               string   `json:"cliName,omitempty"`
	GitHubMarketplace     string   `json:"githubMarketplace,omitempty"`
	ClaudeMarketplace     string   `json:"claudeMarketplace,omitempty"`
	CursorMarketplace     string   `json:"cursorMarketplace,omitempty"`
	LicenseFiles          []string `json:"licenseFiles"`
	Targets               []target `json:"targets"`
	root                  string
}

func loadDefinition(repositoryRoot string) (definition, error) {
	return loadDefinitionAt(repositoryRoot, defaultDefinitionPath)
}

func loadDefinitionAt(repositoryRoot, configPath string) (definition, error) {
	var result definition
	if !localRelativePath(configPath) {
		return result, fmt.Errorf("agentdist config path must be relative to the repository root")
	}
	filename := filepath.Join(repositoryRoot, filepath.FromSlash(configPath))
	body, err := os.ReadFile(filename)
	if err != nil {
		return result, fmt.Errorf("read distribution definition: %w", err)
	}
	if err = decodeStrict(body, &result); err != nil {
		return result, fmt.Errorf("parse distribution definition: %w", err)
	}
	result.root = filepath.Dir(filename)
	if result.SchemaVersion != 1 {
		return result, fmt.Errorf("unsupported agentdist schemaVersion %d", result.SchemaVersion)
	}
	if !validDistributionVersion(result.DefaultVersion) {
		return result, fmt.Errorf("invalid defaultVersion %q", result.DefaultVersion)
	}
	if strings.TrimSpace(result.License) == "" || result.License != strings.TrimSpace(result.License) {
		return result, fmt.Errorf("distribution license must be declared without surrounding whitespace")
	}
	if !localRelativePath(result.PluginRoot) || !localRelativePath(result.SkillsRoot) {
		return result, fmt.Errorf("pluginRoot and skillsRoot must be local relative paths")
	}
	if result.ContractExamples != "" && !localRelativePath(result.ContractExamples) {
		return result, fmt.Errorf("contractExamples must be a local relative path")
	}
	if result.MCPRegistrationSource != "" && !localRelativePath(result.MCPRegistrationSource) || result.CLIHelpSource != "" && !localRelativePath(result.CLIHelpSource) {
		return result, fmt.Errorf("MCP and CLI source paths must be local relative paths")
	}
	for _, source := range []struct {
		name string
		path string
	}{
		{name: "githubMarketplace", path: result.GitHubMarketplace},
		{name: "claudeMarketplace", path: result.ClaudeMarketplace},
		{name: "cursorMarketplace", path: result.CursorMarketplace},
	} {
		if source.path != "" && !localRelativePath(source.path) {
			return result, fmt.Errorf("%s must be a local relative path", source.name)
		}
	}
	if result.ExpectedMCPTools < 0 || result.MCPRegistrationSource == "" && (result.ExpectedMCPTools != 0 || result.ContractExamples != "" || result.MCPToolPrefix != "") || result.MCPRegistrationSource != "" && (result.ExpectedMCPTools == 0 || result.ContractExamples == "" || !mcpToolPrefixPattern.MatchString(result.MCPToolPrefix)) {
		return result, fmt.Errorf("MCP registration source, tool prefix, expected tool count, and contract examples must be declared together")
	}
	if result.CLIHelpSource == "" && (result.CLIHelpConstant != "" || result.CLIName != "") || result.CLIHelpSource != "" && (result.CLIHelpConstant == "" || !cliHelpConstantPattern.MatchString(result.CLIHelpConstant) || !cliNamePattern.MatchString(result.CLIName)) {
		return result, fmt.Errorf("CLI help source, constant, and CLI name must be declared together")
	}
	if len(result.Targets) == 0 || len(result.LicenseFiles) == 0 {
		return result, fmt.Errorf("distribution must declare targets and license files")
	}
	seenIDs := map[string]bool{}
	seenArchives := map[string]bool{}
	for _, item := range result.Targets {
		if !targetIDPattern.MatchString(item.ID) || seenIDs[item.ID] {
			return result, fmt.Errorf("invalid or duplicate target id %q", item.ID)
		}
		if !localRelativePath(item.Source) || !localRelativePath(item.SkillsPath) || seenArchives[item.Archive] {
			return result, fmt.Errorf("target %q has an invalid source, skillsPath, archive name, or duplicate archive", item.ID)
		}
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*_agent_[a-z0-9-]+$`).MatchString(item.Archive) {
			return result, fmt.Errorf("target %q has an invalid archive name %q", item.ID, item.Archive)
		}
		for _, executable := range item.ExecutableFiles {
			if !localRelativePath(executable) {
				return result, fmt.Errorf("target %q has an invalid executable file path %q", item.ID, executable)
			}
		}
		seenIDs[item.ID] = true
		seenArchives[item.Archive] = true
	}
	for _, name := range result.LicenseFiles {
		if !localRelativePath(name) || strings.Contains(name, "/") {
			return result, fmt.Errorf("license file %q must be a root-level relative path", name)
		}
	}
	return result, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func validDistributionVersion(value string) bool {
	return len(value) <= 64 && distributionVersionPattern.MatchString(value)
}

func mcpToolReferencePatternFor(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(prefix) + `[a-z][a-z0-9_]*\b`)
}

func cliCommandReferencePatternFor(name string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `[ \t]+([a-z][a-z-]*)\b`)
}

func localRelativePath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(value))) == value
}
