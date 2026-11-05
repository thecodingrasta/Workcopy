package agentctx

import (
	"bytes"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// What the filters refuse comes in tiers. Instruction files (the default class)
// are refused anything that looks like settings or credentials. Tool
// configuration the owner opts in by name is allowed settings and MCP files but
// still refused credentials unless the owner names that exact file as holding
// secrets. Private keys, keystores, cloud and SSH credential stores, session
// history, caches, media, archives and binaries are refused whatever the
// settings say: no setting can make Workcopy copy those into a lane an agent
// can read.

var deniedDirectories = map[string]bool{
	".git": true, ".ssh": true, ".aws": true, ".gnupg": true, ".azure": true, ".kube": true, "node_modules": true, "sessions": true, "session": true, "history": true, "cache": true, "caches": true,
	"logs": true, "log": true, "tmp": true, "temp": true, "projects": true, "todos": true, "shell-snapshots": true,
	"statsig": true, "telemetry": true, "backups": true, "checkpoints": true, "chats": true, "conversations": true,
	"transcripts": true, "ide": true, "bin": true, "obj": true, "dist": true, "build": true, "target": true,
	".venv": true, "venv": true, "__pycache__": true,
}

// deniedExtensions are media, archives, binaries, data stores and documents.
var deniedExtensions = map[string]bool{}

// allowedExtensions are the text and script types agent instructions use.
var allowedExtensions = map[string]bool{}

func init() {
	for _, e := range strings.Fields(`png jpg jpeg gif webp bmp ico icns tif tiff svg heic avif psd
		mp3 wav ogg flac aac m4a mp4 mov avi mkv webm wmv
		zip tar gz tgz bz2 xz zst 7z rar cab iso dmg
		exe dll so dylib bin o a lib obj class jar war pyc pyo wasm node msi
		db sqlite sqlite3 mdb log jsonl ndjson parquet pkl pickle npy bak swp
		pdf doc docx xls xlsx ppt pptx odt
		ttf otf woff woff2 eot`) {
		deniedExtensions[e] = true
	}
	for _, e := range strings.Fields(`md mdc mdx txt rst yaml yml toml json hook prompt instructions
		sh bash zsh ps1 psm1 cmd bat py js mjs cjs ts rb`) {
		allowedExtensions[e] = true
	}
}

// hardNames are key material and credential stores: never copied.
var hardNames = regexp.MustCompile(`(?i)^(.*\.(pem|key|pfx|p12|ppk|keystore|jks|kdbx|gpg|asc)|id_(rsa|dsa|ecdsa|ed25519)(\..*)?|\.npmrc|\.pypirc|\.netrc|\.aws|\.ssh)$`)

// credentialNames usually hold secrets: copied only when the owner names the
// exact file in the secrets list.
var credentialNames = regexp.MustCompile(`(?i)^(\.env(\..*)?|credentials?(\..*)?|.*secrets?.*|.*\.token|.*tokens?\.(json|txt|yml|yaml)|auth\.json|oauth_creds\.json)$`)

// settingNames are local tool settings and MCP server definitions: copied only
// when the owner opts the file in as configuration.
var settingNames = regexp.MustCompile(`(?i)^(.*\.local\.json|mcp([._-].*)?\.json|mcp_config\.json)$`)
var envTemplates = regexp.MustCompile(`(?i)^\.env\.(example|sample|template|dist)$`)

var secretContent = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}\b`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{24,}\b`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.`),
	regexp.MustCompile(`(?i)\b(?:password|passwd|secret|token|api[_-]?key|access[_-]?key)["']?\s*[:=]\s*["']?([A-Za-z0-9/+_.=-]{20,})`),
}

var placeholder = regexp.MustCompile(`(?i)(your|example|sample|placeholder|changeme|xxxx|\*\*\*\*|<|\$\{|\{\{|%[A-Z_]+%|\$[A-Z_]+)`)

// Class says how much a source of files is trusted by the owner.
type Class int

const (
	// Instructions are files found by the catalogue or the include list.
	Instructions Class = iota
	// Config are files the owner named as tool configuration.
	Config
	// Secrets are exact files the owner accepted may hold credentials.
	Secrets
)

// DenyReason explains why an instruction file may never be copied, or returns "".
func DenyReason(rel string) string { return DenyReasonFor(rel, Instructions) }

// DenyReasonFor explains why a path may not be copied under the given class.
func DenyReasonFor(rel string, class Class) string {
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		if deniedDirectories[strings.ToLower(part)] {
			return "inside a " + part + " directory (state, history, cache, credential store or build output)"
		}
	}
	base := parts[len(parts)-1]
	if hardNames.MatchString(base) {
		return "private key, keystore or credential store; never copied"
	}
	templated := envTemplates.MatchString(base)
	switch class {
	case Instructions:
		if (credentialNames.MatchString(base) || settingNames.MatchString(base)) && !templated {
			return "name suggests credentials or local-only settings"
		}
	case Config:
		if credentialNames.MatchString(base) && !templated {
			return "name suggests credentials; to copy this exact file anyway, list it in the secrets setting"
		}
	}
	if ext := extension(base); deniedExtensions[ext] {
		return "." + ext + " is media, an archive, a binary or a data store"
	}
	return ""
}

func extension(base string) string {
	return strings.TrimPrefix(strings.ToLower(path.Ext(base)), ".")
}

// ExtensionAllowed reports whether the file type is an instruction or script
// format. Files with no extension are allowed only when the catalogue or the
// owner names them, which the caller decides.
func ExtensionAllowed(base string) bool { return allowedExtensions[extension(base)] }

// configExtensions are the settings formats a tool configuration uses, beyond
// the instruction types.
var configExtensions = map[string]bool{"ini": true, "cfg": true, "conf": true, "properties": true, "xml": true, "jsonc": true, "env": true}

// ConfigExtensionAllowed reports whether a file named by a glob (rather than
// literally) is an instruction, script or settings format.
func ConfigExtensionAllowed(base string) bool {
	return allowedExtensions[extension(base)] || configExtensions[extension(base)]
}

// NotText is the reason given for binary or non-UTF-8 content.
const NotText = "not text"

// ContentReason inspects a candidate's bytes and returns why it must not be
// copied, or "". It never returns the matched secret.
func ContentReason(body []byte) string {
	if bytes.IndexByte(body, 0) >= 0 || !utf8.Valid(body) {
		return NotText
	}
	for line, text := range strings.Split(string(body), "\n") {
		for _, pattern := range secretContent {
			match := pattern.FindStringSubmatch(text)
			if match == nil {
				continue
			}
			if len(match) > 1 && placeholder.MatchString(match[1]+text) {
				continue
			}
			return "looks like it contains a credential (line " + strconv.Itoa(line+1) + ")"
		}
	}
	return ""
}
