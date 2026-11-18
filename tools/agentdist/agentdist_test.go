package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryDistributionValidates(t *testing.T) {
	root := repositoryRoot(t)
	if err := validateDistribution(root); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReleaseVersionMatchesGitSource(t *testing.T) {
	root := repositoryRoot(t)
	t.Chdir(root)
	definition, err := loadDefinition(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"validate", "--release-version", definition.DefaultVersion + "-mismatch"}); err == nil || !strings.Contains(err.Error(), "does not match Git-source plugin version") {
		t.Fatalf("mismatched release was accepted: %v", err)
	}
	if err := run([]string{"validate", "--release-version", definition.DefaultVersion}); err != nil {
		t.Fatal(err)
	}
}

func TestPortablePublicationSourceIncludesNotices(t *testing.T) {
	root := repositoryRoot(t)
	d, err := loadDefinition(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range d.Targets {
		if item.ID == "plugin" {
			if !item.PublishSource {
				t.Fatal("portable plugin is not a publication source")
			}
			if err := validatePublishedSourceFiles(root, d, item); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("portable plugin target missing")
}

func TestPackagePreservesBinaryPlaceholder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "distdef")
	files := map[string][]byte{
		"LICENSE": []byte("license"), "distdef/plugin/README.md": []byte("readme"),
		"distdef/plugin/skills/setup/SKILL.md": []byte("skill"),
		"distdef/marketplace-metadata.json":    []byte(`{}`),
		"distdef/adapter/bin/server":           []byte("\x00{{VERSION}}\xff"),
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	d := definition{root: dir, PluginRoot: "plugin", SkillsRoot: "plugin/skills", LicenseFiles: []string{"LICENSE"}, DefaultVersion: "1.0.0"}
	item := target{ID: "fixture", Source: "adapter", SkillsPath: "skills", ExecutableFiles: []string{"bin/server"}}
	result, err := targetPackageFiles(root, dir, d, item, "1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if string(result["bin/server"]) != "\x00{{VERSION}}\xff" {
		t.Fatal("packaging changed binary bytes")
	}
}

func TestPackagesAreReproducibleCompleteAndChecksummed(t *testing.T) {
	root := repositoryRoot(t)
	definition, err := loadDefinition(root)
	if err != nil {
		t.Fatal(err)
	}
	first := t.TempDir()
	second := t.TempDir()
	if err = packageDistribution(root, definition, definition.DefaultVersion, first); err != nil {
		t.Fatal(err)
	}
	if err = packageDistribution(root, definition, definition.DefaultVersion, second); err != nil {
		t.Fatal(err)
	}
	checksums, err := os.ReadFile(filepath.Join(first, "AGENT_PLUGIN_SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(checksums), "\n") {
		t.Fatal("checksum list must end with a newline")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(checksums)), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			t.Fatalf("invalid checksum row %q", line)
		}
		firstBody, readErr := os.ReadFile(filepath.Join(first, parts[1]))
		if readErr != nil {
			t.Fatal(readErr)
		}
		secondBody, readErr := os.ReadFile(filepath.Join(second, parts[1]))
		if readErr != nil {
			t.Fatal(readErr)
		}
		digest := sha256.Sum256(firstBody)
		if hex.EncodeToString(digest[:]) != parts[0] || string(firstBody) != string(secondBody) {
			t.Fatalf("package %s checksum or reproducibility mismatch", parts[1])
		}
	}
	if got, want := strings.Count(string(checksums), "\n"), len(definition.Targets); got != want {
		t.Fatalf("checksummed package count = %d, want %d", got, want)
	}
	for _, item := range definition.Targets {
		filename := item.Archive + "_v" + definition.DefaultVersion + ".zip"
		reader, openErr := zip.OpenReader(filepath.Join(first, filename))
		if openErr != nil {
			t.Fatal(openErr)
		}
		members := map[string]*zip.File{}
		for _, member := range reader.File {
			members[member.Name] = member
		}
		for _, required := range []string{"README.md", "LICENSE", "COPYRIGHT", "MARKETPLACE-METADATA.json"} {
			if _, found := members[required]; !found {
				t.Errorf("%s omitted %s", item.ID, required)
			}
		}
		if item.ID == "plugin" || item.ID == "openai-skills-only" || item.ID == "kiro-power" {
			if _, found := members["plugin.json"]; !found {
				t.Errorf("%s omitted its root plugin.json", item.ID)
			}
		}
		if item.ID == "plugin" || item.ID == "kiro-power" {
			if _, found := members["mcp.json"]; !found {
				t.Errorf("%s portable Agent Plugin omitted mcp.json", item.ID)
			}
		}
		for _, skill := range []string{"workcopy-setup", "workcopy-lane-manager", "workcopy-safe-review", "workcopy-storage-guardian", "workcopy-multi-agent-orchestration", "workcopy-repository-validation"} {
			if _, found := members[item.SkillsPath+"/"+skill+"/SKILL.md"]; !found {
				t.Errorf("%s omitted canonical skill %s", item.ID, skill)
			}
		}
		for _, member := range reader.File {
			if strings.Contains(member.Name, "..") {
				t.Errorf("%s contains escaping archive path %q", item.ID, member.Name)
			}
		}
		if err = reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestContractValidationRejectsUnknownMCPArgument(t *testing.T) {
	root := repositoryRoot(t)
	tools, err := readMCPToolContracts(root)
	if err != nil {
		t.Fatal(err)
	}
	filename, examples := contractExamples(t, root)
	examples["workcopy_destroy"]["force"] = json.RawMessage("true")
	body, err := json.Marshal(examples)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filename, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err = validateContractExamples(filename, tools); err == nil || !strings.Contains(err.Error(), `unsupported argument "force"`) {
		t.Fatalf("unknown MCP argument accepted: %v", err)
	}
}

func TestContractValidationRejectsMissingRequiredMCPArgument(t *testing.T) {
	root := repositoryRoot(t)
	tools, err := readMCPToolContracts(root)
	if err != nil {
		t.Fatal(err)
	}
	filename, examples := contractExamples(t, root)
	delete(examples["workcopy_create"], "dryRun")
	body, err := json.Marshal(examples)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filename, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err = validateContractExamples(filename, tools); err == nil || !strings.Contains(err.Error(), `omits required field "dryRun"`) {
		t.Fatalf("missing required MCP argument accepted: %v", err)
	}
}

func contractExamples(t *testing.T, root string) (string, map[string]map[string]json.RawMessage) {
	t.Helper()
	definition, err := loadDefinition(root)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(definition.root, filepath.FromSlash(definition.ContractExamples)))
	if err != nil {
		t.Fatal(err)
	}
	examples := map[string]map[string]json.RawMessage{}
	if err = json.Unmarshal(body, &examples); err != nil {
		t.Fatal(err)
	}
	for name, fields := range examples {
		copyFields := make(map[string]json.RawMessage, len(fields))
		for key, value := range fields {
			copyFields[key] = value
		}
		examples[name] = copyFields
	}
	return filepath.Join(t.TempDir(), "contracts.json"), examples
}

func TestPluginCommandAndWorkingDirectoryRejectEscape(t *testing.T) {
	if !validPluginCommand("workcopy") || !validPluginCommand("./bin/server") {
		t.Fatal("valid plugin command rejected")
	}
	for _, value := range []string{"workcopy mcp", "../bin/server", "${PLUGIN_ROOT}/server"} {
		if validPluginCommand(value) {
			t.Errorf("unsafe plugin command accepted: %q", value)
		}
	}
	if !validPluginCWD("${PLUGIN_DATA}/lanes") || !validPluginCWD("./data") {
		t.Fatal("valid plugin working directory rejected")
	}
	for _, value := range []string{"${PLUGIN_DATA}/../outside", "${PLUGIN_ROOT}outside", "data"} {
		if validPluginCWD(value) {
			t.Errorf("unsafe plugin working directory accepted: %q", value)
		}
	}
}

func TestOpenCodeAdapterUsesCurrentLocalMCPShape(t *testing.T) {
	valid := map[string][]byte{
		"opencode.json": []byte(`{"mcp":{"servers":{"workcopy":{"type":"local","command":["workcopy","mcp"]}}}}`),
	}
	if err := validateOpenCodeMCPConfig(valid, "opencode.json"); err != nil {
		t.Fatalf("valid OpenCode MCP configuration rejected: %v", err)
	}
	legacy := map[string][]byte{
		"opencode.json": []byte(`{"mcp":{"workcopy":{"type":"local","command":["workcopy","mcp"]}}}`),
	}
	if err := validateOpenCodeMCPConfig(legacy, "opencode.json"); err == nil {
		t.Fatal("legacy direct mcp.workcopy configuration was accepted")
	}
}

func TestWorkcopyOpenCodeExposesDirectTools(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repositoryRoot(t), "agent-distribution", "workcopy", "adapters", "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCP struct {
			Servers map[string]struct {
				CodeMode *bool `json:"codemode"`
			} `json:"servers"`
		} `json:"mcp"`
	}
	if err = json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	server := config.MCP.Servers["workcopy"]
	if server.CodeMode == nil || *server.CodeMode {
		t.Fatal("Workcopy OpenCode tools must be exposed directly")
	}
}

func TestAntigravityPromptsStayWithinMarketplaceLimits(t *testing.T) {
	manifest := []byte(`{"$schema":"https://antigravity.google/schemas/v1/plugin.json","name":"workcopy","displayName":"Workcopy","version":"0.1.5","description":"Workcopy lanes","license":"GPL-3.0-or-later","author":{"name":"Workcopy maintainers"},"suggestedPrompts":["Check setup","Preview a lane","Prepare review"]}`)
	files := map[string][]byte{".antigravity-plugin/plugin.json": manifest}
	if err := validateAntigravityManifest(files, "0.1.5", "GPL-3.0-or-later"); err != nil {
		t.Fatalf("valid Antigravity manifest rejected: %v", err)
	}
	for _, prompts := range [][]string{
		{"Check setup", "Preview a lane", "Prepare review", "Extra prompt"},
		{"Check setup", " ", "Prepare review"},
	} {
		body, err := json.Marshal(struct {
			Schema      string `json:"$schema"`
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
			Version     string `json:"version"`
			Description string `json:"description"`
			License     string `json:"license"`
			Author      struct {
				Name string `json:"name"`
			} `json:"author"`
			SuggestedPrompts []string `json:"suggestedPrompts"`
		}{
			Schema: "https://antigravity.google/schemas/v1/plugin.json",
			Name:   "workcopy", DisplayName: "Workcopy", Version: "0.1.5", Description: "Workcopy lanes", License: "GPL-3.0-or-later",
			Author: struct {
				Name string `json:"name"`
			}{Name: "Workcopy maintainers"},
			SuggestedPrompts: prompts,
		})
		if err != nil {
			t.Fatal(err)
		}
		files[".antigravity-plugin/plugin.json"] = body
		if err = validateAntigravityManifest(files, "0.1.5", "GPL-3.0-or-later"); err == nil {
			t.Errorf("invalid Antigravity suggested prompts accepted: %#v", prompts)
		}
	}
}

func TestRepositoryMarketplaceRequiresContainedMatchingPluginSource(t *testing.T) {
	for _, test := range []struct {
		name      string
		source    string
		wantError bool
	}{
		{name: "dot relative path accepted", source: "./adapter"},
		{name: "wrong adapter refused", source: "./other", wantError: true},
		{name: "parent traversal refused", source: "./../outside", wantError: true},
		{name: "absolute path refused", source: "C:/outside", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var marketplace githubMarketplaceManifest
			body := []byte(`{"name":"workcopy-tools","owner":{"name":"Workcopy maintainers"},"plugins":[{"name":"workcopy","description":"Workcopy plugin","source":"` + test.source + `"}]}`)
			if err := decodeStrict(body, &marketplace); err != nil {
				t.Fatal(err)
			}
			err := validateMarketplaceListing("marketplace.json", marketplace, "workcopy", "0.1.5", "adapter")
			if (err != nil) != test.wantError {
				t.Fatalf("validateMarketplaceListing() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}

func TestPublishedSkillSyncGeneratesChecksAndRefreshesCopies(t *testing.T) {
	repositoryRoot := t.TempDir()
	distributionRoot := filepath.Join(repositoryRoot, "agent-distribution", "product")
	canonicalSkill := filepath.Join(distributionRoot, "skills", "setup", "SKILL.md")
	canonicalReference := filepath.Join(distributionRoot, "skills", "setup", "references", "guide.md")
	adapterRoot := filepath.Join(distributionRoot, "adapters", "claude")
	for _, directory := range []string{filepath.Dir(canonicalSkill), filepath.Dir(canonicalReference), adapterRoot} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(canonicalSkill, []byte("---\nname: setup\ndescription: Setup the tool.\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonicalReference, []byte("Current setup steps.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryRoot, "LICENSE"), []byte("MIT License\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryRoot, "COPYRIGHT"), []byte("Copyright Example\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapterRoot, "README.md"), []byte("# Example adapter\n"), 0644); err != nil {
		t.Fatal(err)
	}
	definition := definition{
		root:         distributionRoot,
		SkillsRoot:   "skills",
		LicenseFiles: []string{"LICENSE", "COPYRIGHT"},
		Targets: []target{{
			ID: "claude", Source: "adapters/claude", SkillsPath: "skills", Archive: "product_agent_claude", PublishSource: true,
		}},
	}
	if err := syncPublishedSkills(repositoryRoot, definition, false); err != nil {
		t.Fatal(err)
	}
	target := definition.Targets[0]
	if err := validatePublishedSkillTree(distributionRoot, definition, target); err != nil {
		t.Fatalf("generated publication source rejected: %v", err)
	}
	if err := validatePublishedSourceFiles(repositoryRoot, definition, target); err != nil {
		t.Fatalf("generated publication notices rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repositoryRoot, "LICENSE"), []byte("Updated MIT License\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := syncPublishedSkills(repositoryRoot, definition, false); err == nil || !strings.Contains(err.Error(), "notice LICENSE") {
		t.Fatalf("stale publication notice was not protected: %v", err)
	}
	if err := syncPublishedSkills(repositoryRoot, definition, true); err != nil {
		t.Fatal(err)
	}
	if err := validatePublishedSourceFiles(repositoryRoot, definition, target); err != nil {
		t.Fatalf("refreshed publication notices rejected: %v", err)
	}
	if err := os.WriteFile(canonicalSkill, []byte("---\nname: setup\ndescription: Updated setup workflow.\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := syncPublishedSkills(repositoryRoot, definition, false); err == nil || !strings.Contains(err.Error(), "--overwrite") {
		t.Fatalf("stale publication copy was not protected: %v", err)
	}
	if err := syncPublishedSkills(repositoryRoot, definition, true); err != nil {
		t.Fatal(err)
	}
	if err := validatePublishedSkillTree(distributionRoot, definition, target); err != nil {
		t.Fatalf("refreshed publication source rejected: %v", err)
	}
	stale := filepath.Join(adapterRoot, "skills", "removed", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(stale), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale generated skill"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := syncPublishedSkills(repositoryRoot, definition, true); err == nil || !strings.Contains(err.Error(), "stale generated skill file") {
		t.Fatalf("stale extra publication file was not detected: %v", err)
	}
}

func TestNativeMarketplaceRequiresConcreteVersionAndCompleteSource(t *testing.T) {
	t.Run("concrete version", func(t *testing.T) {
		root, definition := nativeMarketplaceFixture(t, "{{VERSION}}", true)
		err := validateNativeRepositoryMarketplace(root, definition, ".claude-plugin/marketplace.json", "claude-code", ".claude-plugin/plugin.json", "claude")
		if err == nil || !strings.Contains(err.Error(), "version") {
			t.Fatalf("unrendered marketplace source version was accepted: %v", err)
		}
	})
	t.Run("README and license notice", func(t *testing.T) {
		root, definition := nativeMarketplaceFixture(t, "1.0.0", false)
		err := validateNativeRepositoryMarketplace(root, definition, ".claude-plugin/marketplace.json", "claude-code", ".claude-plugin/plugin.json", "claude")
		if err == nil || !strings.Contains(err.Error(), "README") {
			t.Fatalf("incomplete marketplace source was accepted: %v", err)
		}
	})
}

func nativeMarketplaceFixture(t *testing.T, version string, includeSourceFiles bool) (string, definition) {
	t.Helper()
	root := t.TempDir()
	distributionRoot := filepath.Join(root, "agent-distribution", "product")
	adapterRoot := filepath.Join(distributionRoot, "adapters", "claude-code")
	skillBody := []byte("---\nname: setup\ndescription: Set up the product.\n---\n")
	files := map[string]string{
		filepath.Join(root, "LICENSE"):                                           "MIT License\n",
		filepath.Join(root, "COPYRIGHT"):                                         "Copyright Product maintainers\n",
		filepath.Join(distributionRoot, "plugin", "skills", "setup", "SKILL.md"): string(skillBody),
		filepath.Join(adapterRoot, "skills", "setup", "SKILL.md"):                string(skillBody),
		filepath.Join(root, ".claude-plugin", "marketplace.json"):                `{"name":"product-tools","owner":{"name":"Product maintainers"},"plugins":[{"name":"workcopy","description":"Product plugin","source":"./agent-distribution/product/adapters/claude-code"}]}`,
	}
	manifest, err := json.Marshal(repositoryPluginManifest{Name: "workcopy", Version: version, Description: "Product plugin", License: "MIT"})
	if err != nil {
		t.Fatal(err)
	}
	files[filepath.Join(adapterRoot, ".claude-plugin", "plugin.json")] = string(manifest)
	if includeSourceFiles {
		files[filepath.Join(adapterRoot, "README.md")] = "# Product plugin\n"
		files[filepath.Join(adapterRoot, "LICENSE")] = "MIT License\n"
		files[filepath.Join(adapterRoot, "COPYRIGHT")] = "Copyright Product maintainers\n"
	}
	for filename, body := range files {
		if err = os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filename, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root, definition{
		DefaultVersion:    "1.0.0",
		License:           "MIT",
		SkillsRoot:        "plugin/skills",
		LicenseFiles:      []string{"LICENSE"},
		ClaudeMarketplace: ".claude-plugin/marketplace.json",
		Targets: []target{{
			ID: "claude-code", Source: "adapters/claude-code", SkillsPath: "skills", Archive: "product_agent_claude", PublishSource: true,
		}},
		root: distributionRoot,
	}
}

func TestDistributionToolkitAcceptsProductSpecificMetadataAndPrefixes(t *testing.T) {
	root := t.TempDir()
	directories := []string{
		"agent-distribution/acme/plugin/skills/verify",
		"agent-distribution/acme/adapters/claude-code/.claude-plugin",
		"internal/mcpserver",
		"internal/cli",
		".claude-plugin",
	}
	for _, directory := range directories {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"go.mod":    "module example.com/acme/tool\n\ngo 1.25\n",
		"LICENSE":   "MIT License\n",
		"COPYRIGHT": "Copyright Acme\n",
		"agent-distribution/acme/agentdist.json": `{
  "schemaVersion": 1,
  "defaultVersion": "2.3.4",
  "license": "MIT",
  "pluginRoot": "plugin",
  "skillsRoot": "plugin/skills",
  "contractExamples": "contracts.json",
  "mcpRegistrationSource": "internal/mcpserver/server.go",
  "mcpToolPrefix": "acme_",
  "expectedMcpTools": 1,
  "cliHelpSource": "internal/cli/cli.go",
  "cliHelpConstant": "sampleHelp",
  "cliName": "sample-cli",
  "claudeMarketplace": ".claude-plugin/marketplace.json",
  "licenseFiles": ["LICENSE", "COPYRIGHT"],
  "targets": [{"id":"claude-code","source":"adapters/claude-code","skillsPath":"skills","archive":"acme_agent_claude","publishSource":true}]
}`,
		"agent-distribution/acme/plugin/plugin.json":                              `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"acme-tool","version":"2.3.4","description":"Acme developer tools.","license":"MIT"}`,
		"agent-distribution/acme/plugin/mcp.json":                                 `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"acme":{"type":"stdio","command":"sample-cli","args":["mcp"]}}}`,
		"agent-distribution/acme/plugin/README.md":                                "# Acme developer tools\n",
		"agent-distribution/acme/plugin/skills/verify/SKILL.md":                   "---\nname: verify\ndescription: Verify Acme tool behavior.\n---\nUse `acme_doctor` and `sample-cli version` to check the local installation.\n",
		"agent-distribution/acme/contracts.json":                                  `{"acme_doctor":{"dryRun":false}}`,
		"agent-distribution/acme/marketplace-metadata.json":                       `{"name":"Acme Tool","pluginId":"acme-tool","description":"Acme developer tools.","publisher":"Acme","repository":"https://example.com/acme","homepage":"https://example.com/acme","license":"MIT","categories":["Developer tools"],"keywords":["acme"],"supportedPlatforms":["windows-amd64"],"privacy":"Local MCP execution.","suggestedPrompts":["Check the Acme tool installation."],"visualAssets":[],"screenshots":[]}`,
		"agent-distribution/acme/adapters/claude-code/.claude-plugin/plugin.json": `{"name":"acme-tool","version":"2.3.4","description":"Acme developer tools.","license":"MIT"}`,
		"agent-distribution/acme/adapters/claude-code/.mcp.json":                  `{"mcpServers":{"acme":{"command":"sample-cli","args":["mcp"]}}}`,
		"agent-distribution/acme/adapters/claude-code/README.md":                  "# Acme Claude plugin\n",
		".claude-plugin/marketplace.json":                                         `{"name":"acme-tools","owner":{"name":"Acme"},"plugins":[{"name":"acme-tool","description":"Acme developer tools.","source":"./agent-distribution/acme/adapters/claude-code"}]}`,
		"internal/mcpserver/server.go":                                            "package server\ntype doctorInput struct { DryRun bool `json:\"dryRun,omitempty\"` }\nfunc register() { add(server, \"doctor\", \"Check the tool\", func(ctx context.Context, input doctorInput) {}) }\n",
		"internal/cli/cli.go":                                                     "package cli\nconst sampleHelp = `Usage: sample-cli\nsample-cli version -- show the version\n`\n",
	}
	for relative, body := range files {
		filename := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	definition, err := loadDefinitionAt(root, "agent-distribution/acme/agentdist.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = syncPublishedSkills(root, definition, false); err != nil {
		t.Fatalf("prepare Acme repository publication source: %v", err)
	}
	t.Chdir(root)
	if err := run([]string{"validate", "--config", "agent-distribution/acme/agentdist.json"}); err != nil {
		t.Fatalf("generic product distribution rejected: %v", err)
	}
}

func TestPackagePreservesDeclaredExecutableMode(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "adapter.zip")
	if err := writePackageArchive(filename, map[string][]byte{"bin/workcopy": []byte("binary fixture")}, []string{"bin/workcopy"}); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != 1 || archive.File[0].Mode().Perm() != 0755 {
		t.Fatalf("packaged executable permissions = %v, want 0755", archive.File[0].Mode().Perm())
	}
}

func TestAgentPluginNameFollowsVersionedSpecConstraints(t *testing.T) {
	for _, name := range []string{"workcopy", "tools.linter", "a1"} {
		if !validAgentPluginName(name) {
			t.Errorf("valid Agent Plugin name rejected: %q", name)
		}
	}
	for _, name := range []string{"", "Workcopy", "-start", "end-", "too--many", "too..many", strings.Repeat("a", 65)} {
		if validAgentPluginName(name) {
			t.Errorf("invalid Agent Plugin name accepted: %q", name)
		}
	}
}

func TestDistributionVersionRejectsPathAndShellSyntax(t *testing.T) {
	for _, version := range []string{"../v1.0.0", "v1.0.0", "1.0.0;whoami", "1.0", "1.0.0+build"} {
		if validDistributionVersion(version) {
			t.Errorf("accepted unsafe distribution version %q", version)
		}
	}
	for _, version := range []string{"0.1.5", "1.2.3-rc.1"} {
		if !validDistributionVersion(version) {
			t.Errorf("rejected valid distribution version %q", version)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for directory := current; ; directory = filepath.Dir(directory) {
		body, readErr := os.ReadFile(filepath.Join(directory, "go.mod"))
		if readErr == nil && strings.HasPrefix(string(body), "module workcopy\n") {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
	}
}
