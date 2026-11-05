package eol

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"workcopy/internal/testutil"
)

func TestToLaneAndNormalizeAreInverseOnLFContent(t *testing.T) {
	for _, body := range []string{"", "\n", "a", "a\n", "a\nb", "a\n\nb\n", "é\nü\n"} {
		lane := ToLane([]byte(body), CRLF)
		if got := Normalize(lane); string(got) != body {
			t.Fatalf("round trip of %q gave %q", body, got)
		}
		if bytes.Contains(lane, []byte("\r\r")) {
			t.Fatalf("doubled CR for %q", body)
		}
		if raw := ToLane([]byte(body), LF); string(raw) != body {
			t.Fatalf("LF policy must not rewrite %q", body)
		}
	}
	if got := ToLane([]byte("a\r\nb\n"), CRLF); string(got) != "a\r\nb\r\n" {
		t.Fatalf("existing CRLF must not double: %q", got)
	}
}

func TestForBlobKeepsAnythingThatCouldNotRoundTripRaw(t *testing.T) {
	cases := []struct {
		name, token, blob, want string
	}{
		{"plain text becomes CRLF", CRLF, "a\nb\n", CRLF},
		{"text with an existing CR stays raw", CRLF, "a\r\nb\n", Raw},
		{"lone CR stays raw", LF, "a\rb\n", Raw},
		{"auto text resolves", AutoCRLF, "a\nb\n", CRLF},
		{"auto binary stays raw", AutoCRLF, "a\x00b\n", Raw},
		{"auto mostly non-printable stays raw", AutoLF, "\x01\x02\x03\x04a\n", Raw},
		{"explicit text keeps NUL content text", CRLF, "a\x00b\n", CRLF},
		{"raw stays raw", Raw, "a\nb\n", Raw},
	}
	for _, c := range cases {
		if got := ForBlob(c.token, []byte(c.blob)); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	if got := ForLocal(AutoCRLF, []byte("x\r\ny\r\n")); got != CRLF {
		t.Errorf("local CRLF text under an auto rule must normalise, got %q", got)
	}
	if got := ForLocal(AutoLF, []byte("x\x00y")); got != Raw {
		t.Errorf("local binary must stay raw, got %q", got)
	}
}

// The decisive test: for a matrix of attributes, configuration and content,
// the bytes this package would write must equal the bytes `git checkout`
// writes. Mixed-ending content is the one documented deviation: Git converts
// the lone LFs of a blob that already has CRs, this package leaves it exactly
// as committed so a fresh lane is never dirty.
func TestParityWithGitCheckout(t *testing.T) {
	t.Parallel()
	attributeSets := []string{
		"", "* text", "* -text", "* text=auto", "* text=auto eol=lf", "* text=auto eol=crlf",
		"* text eol=lf", "* text eol=crlf", "* eol=crlf", "* eol=lf", "* text=input",
		"* crlf", "* -crlf", "* crlf=input", "* binary", "* text=crlf", "* -text eol=crlf",
	}
	contents := map[string]string{
		"text": "a\nb\n", "noeol": "a\nb", "empty": "", "blank": "\n", "nulbyte": "a\x00b\n",
		"control": "\x01\x02\x03\x04a\n", "utf8": "é\nü\n", "del": "a\x7f\nb\n", "ctrlz": "a\nb\n\x1a",
		"crlf": "a\r\nb\r\n", "mixed": "a\r\nb\n", "lonecr": "a\rb\n",
	}
	repo := testutil.NewRepository(t)
	type entry struct{ dir, content string }
	var entries []entry
	for ai, attrs := range attributeSets {
		for name, content := range contents {
			dir := fmt.Sprintf("a%02d/%s", ai, name)
			repo.Write(dir+"/.gitattributes", attrs+"\n")
			repo.Write(dir+"/file", content)
			entries = append(entries, entry{dir, content})
		}
	}
	// Git normalises on add, so store the exact bytes with --no-filters. One
	// batched process per step keeps the corpus fast on Windows.
	var files, attributeFiles []string
	for _, e := range entries {
		files = append(files, e.dir+"/file")
		attributeFiles = append(attributeFiles, e.dir+"/.gitattributes")
	}
	var index strings.Builder
	for _, group := range [][]string{files, attributeFiles} {
		ids := strings.Fields(repo.GitInput(strings.Join(group, "\n")+"\n", "hash-object", "-w", "--no-filters", "--stdin-paths"))
		if len(ids) != len(group) {
			t.Fatalf("hash-object returned %d ids for %d paths", len(ids), len(group))
		}
		for n, path := range group {
			fmt.Fprintf(&index, "100644 %s\t%s\x00", ids[n], path)
		}
	}
	repo.GitInput(index.String(), "update-index", "--add", "-z", "--index-info")
	repo.Git("commit", "-m", "corpus")
	repo.Git("config", "--unset", "core.autocrlf")
	attributes := checkAttrs(t, repo, files)
	for _, autocrlf := range []string{"", "true", "false", "input"} {
		for _, coreEOL := range []string{"", "lf", "crlf", "native"} {
			cfg := Config{AutoCRLF: "false", EOL: "native", NativeCRLF: runtime.GOOS == "windows"}
			args := []string{}
			if autocrlf != "" {
				args = append(args, "-c", "core.autocrlf="+autocrlf)
				cfg.AutoCRLF = autocrlf
			}
			if coreEOL != "" {
				args = append(args, "-c", "core.eol="+coreEOL)
				cfg.EOL = coreEOL
			}
			prefix := fmt.Sprintf("out-%s-%s/", autocrlf, coreEOL)
			git := exec.Command("git", append(append([]string{"-C", repo.Path}, args...), "checkout-index", "--all", "--force", "--prefix="+prefix)...)
			git.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
			if body, err := git.CombinedOutput(); err != nil {
				t.Fatalf("checkout-index: %v\n%s", err, body)
			}
			for _, e := range entries {
				attrs := attributes[e.dir+"/file"]
				token := ForBlob(Resolve(attrs, cfg), []byte(e.content))
				got := ToLane([]byte(e.content), token)
				want, err := os.ReadFile(filepath.Join(repo.Path, filepath.FromSlash(prefix+e.dir+"/file")))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(e.content, "\r") {
					if string(got) != e.content {
						t.Fatalf("%s %+v cfg=%+v: content with CR must stay as committed, got %q", e.dir, attrs, cfg, got)
					}
					continue
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("%s attrs=%+v autocrlf=%q eol=%q: lane would write %q but git writes %q (token %q)", e.dir, attrs, autocrlf, coreEOL, got, want, token)
				}
			}
		}
	}
}

func checkAttrs(t *testing.T, repo *testutil.Repository, paths []string) map[string]Attributes {
	t.Helper()
	command := exec.Command("git", "-C", repo.Path, "check-attr", "--all", "-z", "--stdin")
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	command.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	body, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]Attributes{}
	fields := bytes.Split(body, []byte{0})
	for n := 0; n+2 < len(fields); n += 3 {
		path := string(fields[n])
		current := result[path]
		switch string(fields[n+1]) {
		case "text":
			current.Text = string(fields[n+2])
		case "crlf":
			current.CRLF = string(fields[n+2])
		case "eol":
			current.EOL = string(fields[n+2])
		}
		result[path] = current
	}
	return result
}
