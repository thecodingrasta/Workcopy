package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
)

func TestHelpDoesNotNeedRepository(t *testing.T) {
	var out, diagnostics bytes.Buffer
	code := Run(context.Background(), []string{"--help"}, &out, &diagnostics)
	if code != 0 || out.Len() == 0 {
		t.Fatalf("help: exit %d, output %q", code, out.String())
	}
}

func TestJSONErrorsMatchProcessExit(t *testing.T) {
	var out, diagnostics bytes.Buffer
	code := Run(context.Background(), []string{"create", "--json", "--unknown"}, &out, &diagnostics)
	var response struct {
		SchemaVersion int `json:"schemaVersion"`
		ExitCode      int `json:"exitCode"`
		Error         *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err, out.String())
	}
	if code == 0 || response.ExitCode != code || response.Error == nil {
		t.Fatalf("inconsistent error: %d %s", code, out.String())
	}
}

func TestParserAcceptsInterspersedRepeatedProjects(t *testing.T) {
	parsed, err := parse([]string{"create", "daily", "--project", "A", "--json", "--project=B"})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.values["project"]) != 2 || !parsed.boolean("json") || len(parsed.positionals) != 1 {
		t.Fatalf("unexpected parse: %#v", parsed)
	}
}

func TestParserRejectsMissingValueAndDuplicateSingleton(t *testing.T) {
	for _, arguments := range [][]string{{"create", "x", "--repo"}, {"create", "x", "--repo=a", "--repo=b"}, {"status", "x", "--json=false"}} {
		if _, err := parse(arguments); err == nil {
			t.Fatalf("accepted invalid arguments: %v", arguments)
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }
func TestHumanOutputFailureReturnsOperationalFailure(t *testing.T) {
	if code := Run(context.Background(), []string{"version"}, brokenWriter{}, io.Discard); code != 5 {
		t.Fatalf("broken stdout returned %d instead of 5", code)
	}
	if code := Run(context.Background(), []string{"not-a-command"}, io.Discard, brokenWriter{}); code != 5 {
		t.Fatalf("broken stderr returned %d instead of 5", code)
	}
}
