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
	"unicode/utf8"
)

const SchemaVersion = 1
const MaxFileBytes int64 = 64 << 20
const MaxMetadataBytes int64 = 16 << 20

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
}

func Version(kind FileKind, mode uint32, body []byte) FileVersion {
	if kind == Missing {
		return FileVersion{Kind: Missing}
	}
	return FileVersion{Kind: kind, Mode: mode, Hash: Hash(body), Size: int64(len(body))}
}
func (v FileVersion) Exists() bool { return v.Kind == Regular || v.Kind == Symlink }
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
type Config struct {
	SchemaVersion  int                `json:"schemaVersion"`
	DefaultProfile string             `json:"defaultProfile,omitempty"`
	Discovery      Discovery          `json:"discovery,omitempty"`
	Projects       []Project          `json:"projects,omitempty"`
	Profiles       map[string]Profile `json:"profiles,omitempty"`
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
type Manifest struct {
	SchemaVersion         int       `json:"schemaVersion"`
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	WorkspacePath         string    `json:"workspacePath"`
	RepositoryRoot        string    `json:"repositoryRoot"`
	CommonGitDir          string    `json:"commonGitDir"`
	WorkRoot              string    `json:"workRoot"`
	Branch                string    `json:"branch"`
	SyncedCommit          string    `json:"syncedCommit"`
	PendingCommit         string    `json:"pendingCommit,omitempty"`
	Generation            uint64    `json:"generation"`
	Phase                 string    `json:"phase"`
	RespectExecutableBits bool      `json:"respectExecutableBits"`
	Config                Config    `json:"config"`
	Selection             Selection `json:"selection"`
	Mappings              []Mapping `json:"mappings,omitempty"`
	Reviews               []Review  `json:"reviews,omitempty"`
}
type Baseline struct {
	Files map[string]FileVersion `json:"files"`
}
type Snapshot struct {
	Manifest  Manifest   `json:"manifest"`
	Baseline  Baseline   `json:"baseline"`
	Conflicts []Conflict `json:"conflicts"`
}
