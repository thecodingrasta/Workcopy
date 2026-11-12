// Package model defines the on-disk and machine-readable contracts.
package model

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

const SchemaVersion = 1
const MaxMetadataBytes int64 = 16 << 20

// A file is handled one of two ways, by size.
//
// Up to InlineFileBytes it is read into memory: hashed, compared, line-ending
// converted and merged as text. Above that it is "large": streamed from disk or
// from Git in fixed-size pieces, hashed as it goes, never held whole, never
// line-ending converted and never text-merged (two edits to one large file are a
// conflict, resolved by choosing a side). Memory use therefore does not grow
// with the size of the biggest file in a repository or in build output.
//
// MaxFileBytes is the separate hard limit, above which Workcopy refuses a
// tracked file instead of copying it. Both can be changed; neither is a limit on
// what a lane may contain, only on what Workcopy will copy into one.
const (
	DefaultInlineFileBytes int64 = 64 << 20
	DefaultMaxFileBytes    int64 = 2 << 30
	// MaxFileMiBKey is the settings key and environment variable for the hard limit.
	MaxFileMiBKey = "WORKCOPY_MAX_FILE_MIB"
)

var (
	inlineFileBytes atomic.Int64
	maxFileBytes    atomic.Int64
)

func init() {
	inlineFileBytes.Store(DefaultInlineFileBytes)
	maxFileBytes.Store(DefaultMaxFileBytes)
	if value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(MaxFileMiBKey)), 10, 64); err == nil && value >= 64 && value <= 1<<20 {
		maxFileBytes.Store(value << 20)
	}
}

// InlineFileBytes is the largest file Workcopy keeps in memory.
func InlineFileBytes() int64 { return inlineFileBytes.Load() }

// MaxFileBytes is the largest tracked file Workcopy will copy into a lane.
func MaxFileBytes() int64 { return maxFileBytes.Load() }

// SetMaxFileBytes changes the hard limit and returns a function that restores it.
func SetMaxFileBytes(value int64) func() {
	previous := maxFileBytes.Swap(value)
	return func() { maxFileBytes.Store(previous) }
}

// SetInlineFileBytes changes the streaming threshold and returns a function that
// restores it. Tests lower it so that small files exercise the streaming paths.
func SetInlineFileBytes(value int64) func() {
	previous := inlineFileBytes.Swap(value)
	return func() { inlineFileBytes.Store(previous) }
}

type Failure struct {
	Exit                      int
	Code, Message, Suggestion string
}

func (f *Failure) Error() string { return f.Message }
func Fail(exit int, code, message string) error {
	return &Failure{Exit: exit, Code: code, Message: message}
}
func Hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func ID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func ValidID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9') && !(ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}
func DecodeStrict(data []byte, value any) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("JSON must be valid UTF-8")
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("expected a JSON object")
	}
	tokens := json.NewDecoder(bytes.NewReader(data))
	tokens.UseNumber()
	if err := uniqueKeys(tokens, 0); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON value")
	}
	return nil
}

func uniqueKeys(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds 64 levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for decoder.More() {
			token, err = decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || keys[key] {
				return fmt.Errorf("duplicate or invalid JSON property %q", key)
			}
			keys[key] = true
			if err = uniqueKeys(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err = uniqueKeys(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}

type FileKind string

const (
	Missing FileKind = "missing"
	Regular FileKind = "file"
	Symlink FileKind = "symlink"
)

type FileVersion struct {
	Kind     FileKind `json:"kind"`
	Mode     uint32   `json:"mode,omitempty"`
	Hash     string   `json:"sha256,omitempty"`
	ObjectID string   `json:"objectId,omitempty"`
	Size     int64    `json:"size,omitempty"`
	// Eol marks a text file whose carriage returns are not content: "lf" or
	// "crlf" (the form written to the lane). Hash and Size always describe the
	// normalised (LF) bytes Git stores, so line endings never cause a change or
	// a merge conflict. Empty means bytes are compared exactly. Equal ignores it.
	Eol string `json:"eol,omitempty"`
}

func Version(kind FileKind, mode uint32, body []byte) FileVersion {
	if kind == Missing {
		return FileVersion{Kind: Missing}
	}
	return FileVersion{Kind: kind, Mode: mode, Hash: Hash(body), Size: int64(len(body))}
}
func (v FileVersion) Exists() bool { return v.Kind == Regular || v.Kind == Symlink }

// Large reports whether the file is handled by streaming rather than in memory.
// Hash and Size then describe the raw bytes: no line-ending conversion applies.
func (v FileVersion) Large() bool { return v.Kind == Regular && v.Size > InlineFileBytes() }
func (v FileVersion) Equal(other FileVersion) bool {
	if !v.Exists() && !other.Exists() {
		return true
	}
	return v.Kind == other.Kind && v.Mode == other.Mode && v.Hash == other.Hash && v.Size == other.Size
}

type Project struct {
	ID           string   `json:"id"`
	Path         string   `json:"path"`
	Dependencies []string `json:"dependencies,omitempty"`
	Kind         string   `json:"kind,omitempty"`
}
type Profile struct {
	Projects          []string `json:"projects,omitempty"`
	Include           []string `json:"include,omitempty"`
	Exclude           []string `json:"exclude,omitempty"`
	DependencyClosure string   `json:"dependencyClosure,omitempty"`
}
type Discovery struct {
	Adapters []string `json:"adapters,omitempty"`
}

// DestroyPolicy lists local-only files (never tracked source) that deletion may
// discard without --force, typically build output. Only an owner-supplied
// configuration is trusted to grant this; a committed .workcopy.json is not.
type DestroyPolicy struct {
	Discard []string `json:"discard,omitempty"`
}
type Config struct {
	SchemaVersion  int                `json:"schemaVersion"`
	DefaultProfile string             `json:"defaultProfile,omitempty"`
	Discovery      Discovery          `json:"discovery,omitempty"`
	Projects       []Project          `json:"projects,omitempty"`
	Profiles       map[string]Profile `json:"profiles,omitempty"`
	Destroy        *DestroyPolicy     `json:"destroy,omitempty"`
}
type Selection struct {
	All      bool     `json:"all"`
	Projects []string `json:"projects,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	Include  []string `json:"include,omitempty"`
	Exclude  []string `json:"exclude,omitempty"`
}
type Mapping struct {
	Path               string            `json:"path"`
	Target             string            `json:"target"`
	Strategy           string            `json:"strategy"`
	Protection         string            `json:"protection"`
	Inputs             map[string]string `json:"inputs"`
	Platform           string            `json:"platform"`
	DependencyIdentity string            `json:"dependencyIdentity"`
	Fingerprint        string            `json:"fingerprint"`
}
type Review struct {
	ID         string                 `json:"id"`
	Path       string                 `json:"path"`
	State      string                 `json:"state"`
	BaseCommit string                 `json:"baseCommit"`
	Commit     string                 `json:"commit,omitempty"`
	Generation uint64                 `json:"generation"`
	Files      map[string]FileVersion `json:"files"`
}
type Conflict struct {
	ID             string      `json:"id"`
	Path           string      `json:"path"`
	Reason         string      `json:"reason"`
	Base           FileVersion `json:"base"`
	Local          FileVersion `json:"local"`
	Upstream       FileVersion `json:"upstream"`
	UpstreamCommit string      `json:"upstreamCommit"`
}

// ContextFile records an agent instruction file copied from the main checkout.
// Hash is the hash of the bytes as copied, so a later refresh can tell an
// untouched copy from one a person or agent edited in the lane.
type ContextFile struct {
	Path   string `json:"path"`
	Hash   string `json:"sha256"`
	Size   int64  `json:"size"`
	Source string `json:"source,omitempty"`
	// Sensitive marks a copy the owner accepted may hold credentials.
	Sensitive bool `json:"sensitive,omitempty"`
}
type Manifest struct {
	SchemaVersion         int    `json:"schemaVersion"`
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	WorkspacePath         string `json:"workspacePath"`
	RepositoryRoot        string `json:"repositoryRoot"`
	CommonGitDir          string `json:"commonGitDir"`
	WorkRoot              string `json:"workRoot"`
	Branch                string `json:"branch"`
	SyncedCommit          string `json:"syncedCommit"`
	PendingCommit         string `json:"pendingCommit,omitempty"`
	Generation            uint64 `json:"generation"`
	Phase                 string `json:"phase"`
	RespectExecutableBits bool   `json:"respectExecutableBits"`
	// OwnerConfig records that Config came from a file the owner chose
	// (--config, or the MCP server's --config) rather than from the repository.
	OwnerConfig bool      `json:"ownerConfig,omitempty"`
	Config      Config    `json:"config"`
	Selection   Selection `json:"selection"`
	Mappings    []Mapping `json:"mappings,omitempty"`
	Reviews     []Review  `json:"reviews,omitempty"`
	// AgentContext lists copied agent instruction files. They are not source:
	// they are excluded from status, reviews and promotion, and destroy
	// discards them.
	AgentContext []ContextFile `json:"agentContext,omitempty"`
	// ContextAgents records which agent tools this lane serves (tool keys, or
	// "all"), settled when the lane was created, so every refresh stays
	// consistent. Empty on lanes made before the selection existed, which
	// received every tool.
	ContextAgents []string `json:"contextAgents,omitempty"`
}
type Baseline struct {
	Files map[string]FileVersion `json:"files"`
}
type Snapshot struct {
	Manifest  Manifest   `json:"manifest"`
	Baseline  Baseline   `json:"baseline"`
	Conflicts []Conflict `json:"conflicts"`
}
