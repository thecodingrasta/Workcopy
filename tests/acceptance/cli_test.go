package acceptance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"workcopy/internal/model"
	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

var executable string

const testOutputMarker = "Workcopy CLI acceptance test output\n"

func TestMain(tests *testing.M) {
	directory, err := os.MkdirTemp("", "wc-cli-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = os.WriteFile(filepath.Join(directory, ".workcopy-test-owner"), []byte(testOutputMarker), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	executable = filepath.Join(directory, "workcopy")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	command := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", executable, "../../cmd/workcopy")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		if cleanupErr := removeTestOutput(directory); cleanupErr != nil {
			fmt.Fprintln(os.Stderr, cleanupErr)
		}
		os.Exit(1)
	}
	code := tests.Run()
	if err = removeTestOutput(directory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func removeTestOutput(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to remove unowned CLI test output directory")
	}
	marker := filepath.Join(directory, ".workcopy-test-owner")
	markerInfo, err := os.Lstat(marker)
	if err != nil || !markerInfo.Mode().IsRegular() {
		return fmt.Errorf("refusing to remove CLI test output without its ownership marker")
	}
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != testOutputMarker {
		return fmt.Errorf("refusing to remove CLI test output with an invalid ownership marker")
	}
	return os.RemoveAll(directory)
}
func invoke(t *testing.T, directory string, expected int, arguments ...string) workspace.Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, append(arguments, "--json")...)
	command.Dir = directory
	body, err := command.Output()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	var result workspace.Report
	if err = json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %s: %v", body, err)
	}
	if result.ExitCode != code || code != expected {
		t.Fatalf("%v: expected %d; process %d; JSON %s", arguments, expected, code, body)
	}
	if result.SchemaVersion != 1 {
		t.Fatalf("missing versioned envelope: %s", body)
	}
	return result
}
func TestDocumentedCLIConflictReviewAndIntegration(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("README.md", "Example repository\n")
	r.Write("A/a.txt", "base\n")
	r.Write("B/b.txt", "second\n")
	r.Write(".gitignore", "packages/\n")
	r.Write(".workcopy.json", `{"schemaVersion":1,"defaultProfile":"daily","discovery":{"adapters":["generic"]},"projects":[{"id":"A","path":"A"},{"id":"B","path":"B"}],"profiles":{"daily":{"projects":["A"],"dependencyClosure":"strict"}}}`)
	r.Commit("base")
	root := filepath.Join(r.Parent, "working lanes")
	invoke(t, r.Path, 0, "doctor")
	invoke(t, r.Path, 0, "discover")
	invoke(t, r.Path, 0, "create", "daily", "--root", root, "--dry-run")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("dry run created workspace root")
	}
	created := invoke(t, r.Path, 0, "create", "daily", "--root", root)
	lane := created.Workspace.Path
	if _, err := os.Stat(filepath.Join(lane, "B")); !os.IsNotExist(err) {
		t.Fatal("unselected project materialized")
	}
	invoke(t, lane, 0, "expand", "B")
	invoke(t, lane, 4, "expand", "B")
	invoke(t, lane, 0, "status")
	cache := filepath.Join(r.Parent, "dependency cache")
	os.Mkdir(cache, 0700)
	os.WriteFile(filepath.Join(cache, "keep"), []byte("do not remove"), 0600)
	invoke(t, lane, 0, "share", "--path", "packages", "--target", cache, "--dependency-identity", "fixture-lock", "--trust-shared")
	invoke(t, lane, 0, "unshare", "--path", "packages")
	if body, _ := os.ReadFile(filepath.Join(cache, "keep")); string(body) != "do not remove" {
		t.Fatal("shared directory damaged")
	}
	os.WriteFile(filepath.Join(lane, "A", "a.txt"), []byte("local\n"), 0644)
	r.Write("A/a.txt", "upstream\n")
	r.Commit("advance upstream")
	sync := invoke(t, lane, 1, "sync")
	if len(sync.Conflicts) != 1 {
		t.Fatal("conflict missing", sync)
	}
	token := sync.Conflicts[0].ID
	content := invoke(t, lane, 0, "content", token, "--side", "base")
	decoded, err := base64.StdEncoding.DecodeString(content.Content.Data)
	if err != nil || string(decoded) != "base\n" || model.Hash(decoded) != content.Content.SHA256 {
		t.Fatal("base content provenance invalid")
	}
	invoke(t, lane, 0, "resolve", "A/a.txt", "--expect-conflict", token, "--use-local")
	status := invoke(t, lane, 0, "status")
	if len(status.Changes) == 0 {
		t.Fatal("resolved work lost from promotion accounting")
	}
	review := invoke(t, lane, 0, "merge", "--no-commit")
	mainBefore := r.Git("rev-parse", "HEAD")
	committed := invoke(t, lane, 0, "merge", "--commit", "--approve", "--review", review.Review.ID, "--message", "Reviewed contribution")
	if r.Git("rev-parse", "HEAD") != mainBefore {
		t.Fatal("Workcopy advanced main")
	}
	invoke(t, lane, 3, "destroy")
	// Integration is an explicit human Git operation, not a Workcopy side effect.
	r.Git("cherry-pick", committed.Review.Commit)
	invoke(t, lane, 0, "sync")
	invoke(t, lane, 0, "destroy")
	if _, err = os.Stat(lane); !os.IsNotExist(err) {
		t.Fatal("lane remains")
	}
	listed := invoke(t, r.Path, 0, "list", "--root", root)
	if len(listed.Workspaces) != 0 {
		t.Fatal("deleted lane still registered")
	}
	if _, err = os.Stat(review.Review.Path); err != nil {
		t.Fatal("deletion removed separate review")
	}
}

func TestLargeMonorepoSupportsThreeIndependentAgentLanes(t *testing.T) {
	r := testutil.NewFixtureRepository(t, "large-monorepo")
	root := filepath.Join(r.Parent, "agent lanes")
	discovered := invoke(t, r.Path, 0, "discover")
	if discovered.Catalog == nil || len(discovered.Catalog.Projects) != 25 {
		count := 0
		if discovered.Catalog != nil {
			count = len(discovered.Catalog.Projects)
		}
		t.Fatalf("expected all 25 configured projects, got %d", count)
	}
	paths := strings.Split(r.Git("ls-tree", "-r", "--name-only", "HEAD"), "\n")
	if len(paths) < 125 {
		t.Fatalf("fixture no longer exercises a substantial source tree: %d tracked paths", len(paths))
	}
	dependencies := map[string][]string{}
	for _, project := range discovered.Catalog.Projects {
		dependencies[project.ID] = project.Dependencies
	}
	if len(dependencies["project-03"]) != 1 || dependencies["project-03"][0] != "project-02" {
		t.Fatalf(".NET project reference was not discovered: %+v", dependencies["project-03"])
	}
	agents := []struct {
		name    string
		project string
		file    string
	}{
		{name: "agent-api", project: "project-01", file: "src/project-01/Component01.cs"},
		{name: "agent-search", project: "project-04", file: "src/project-04/Component04.cs"},
		{name: "agent-ui", project: "project-07", file: "src/project-07/Component07.cs"},
	}
	lanePaths := make(map[string]string, len(agents))
	for _, agent := range agents {
		created := invoke(t, r.Path, 0, "create", agent.name, "--root", root, "--project", agent.project)
		lane := created.Workspace.Path
		lanePaths[agent.name] = lane
		projects, err := os.ReadDir(filepath.Join(lane, "src"))
		if err != nil || len(projects) != 1 || projects[0].Name() != agent.project {
			t.Fatalf("%s materialized the wrong project set: %+v (%v)", agent.name, projects, err)
		}
		if _, err = os.Stat(filepath.Join(lane, filepath.FromSlash(agent.file))); err != nil {
			t.Fatalf("selected project source missing from %s: %v", agent.name, err)
		}
		if _, err := os.Stat(filepath.Join(lane, ".git")); !os.IsNotExist(err) {
			t.Fatalf("agent lane %s contains a Git repository", agent.name)
		}
	}

	var writers sync.WaitGroup
	writeErrors := make(chan error, len(agents))
	for _, agent := range agents {
		agent := agent
		writers.Add(1)
		go func() {
			defer writers.Done()
			lane := lanePaths[agent.name]
			edit := []byte("// contribution from " + agent.name + "\n" + agent.project + "\n")
			writeErrors <- os.WriteFile(filepath.Join(lane, filepath.FromSlash(agent.file)), edit, 0644)
		}()
	}
	writers.Wait()
	close(writeErrors)
	for err := range writeErrors {
		if err != nil {
			t.Fatal(err)
		}
	}

	upstreamPath := agents[0].file
	upstreamBody := []byte("// upstream change from owner\nproject-01\n")
	r.Write(upstreamPath, string(upstreamBody))
	r.Commit("upstream change collides with agent-api")
	mainHead := r.Git("rev-parse", "HEAD")
	mainIndex := r.Git("write-tree")

	conflict := invoke(t, lanePaths[agents[0].name], 1, "sync")
	if len(conflict.Conflicts) != 1 || conflict.Conflicts[0].Path != upstreamPath {
		t.Fatalf("expected the deliberate agent-api conflict, got %+v", conflict.Conflicts)
	}
	token := conflict.Conflicts[0].ID
	for side, expected := range map[string]string{
		"base":     "Component01",
		"local":    "contribution from agent-api",
		"upstream": "upstream change from owner",
	} {
		content := invoke(t, lanePaths[agents[0].name], 0, "content", token, "--side", side)
		decoded, err := base64.StdEncoding.DecodeString(content.Content.Data)
		if err != nil || !strings.Contains(string(decoded), expected) || model.Hash(decoded) != content.Content.SHA256 {
			t.Fatalf("%s conflict content or digest is wrong: %q: %v", side, decoded, err)
		}
	}
	resolvedPath := filepath.Join(r.Parent, "resolved-component01.cs")
	resolvedBody := []byte("// contribution from agent-api\n// upstream change from owner\nproject-01\n")
	if err := os.WriteFile(resolvedPath, resolvedBody, 0600); err != nil {
		t.Fatal(err)
	}
	invoke(t, lanePaths[agents[0].name], 0, "resolve", upstreamPath, "--expect-conflict", token, "--from-file", resolvedPath)
	resolved, err := os.ReadFile(filepath.Join(lanePaths[agents[0].name], filepath.FromSlash(upstreamPath)))
	if err != nil || string(resolved) != string(resolvedBody) {
		t.Fatalf("resolution did not retain both local and upstream bytes: %q: %v", resolved, err)
	}

	type prepared struct {
		commit, reviewPath string
	}
	preparedReviews := make([]prepared, 0, len(agents))
	reviewPaths := make(map[string]bool, len(agents))
	for _, agent := range agents {
		lane := lanePaths[agent.name]
		status := invoke(t, lane, 0, "status")
		if len(status.Changes) != 1 || status.Changes[0].Path != agent.file {
			t.Fatalf("%s changes escaped its assigned project: %+v", agent.name, status.Changes)
		}
		review := invoke(t, lane, 0, "merge", "--no-commit")
		if review.Review == nil || review.Review.Commit != "" {
			t.Fatalf("%s merge did not prepare an uncommitted review: %+v", agent.name, review.Review)
		}
		if reviewPaths[review.Review.Path] {
			t.Fatalf("lanes reused one review path: %s", review.Review.Path)
		}
		reviewPaths[review.Review.Path] = true
		if _, err = os.Stat(filepath.Join(review.Review.Path, filepath.FromSlash(agent.file))); err != nil {
			t.Fatalf("%s review is missing its contribution: %v", agent.name, err)
		}
		reviewedUpstream, err := os.ReadFile(filepath.Join(review.Review.Path, filepath.FromSlash(upstreamPath)))
		if err != nil {
			t.Fatalf("%s review is missing the upstream path: %v", agent.name, err)
		}
		if agent.name == agents[0].name && string(reviewedUpstream) != string(resolvedBody) {
			t.Fatalf("agent-api review lost resolved bytes: %q", reviewedUpstream)
		}
		if agent.name != agents[0].name && string(reviewedUpstream) != string(upstreamBody) {
			t.Fatalf("%s review lost the independent upstream edit: %q", agent.name, reviewedUpstream)
		}
		committed := invoke(t, lane, 0, "merge", "--commit", "--approve", "--review", review.Review.ID, "--message", "Reviewed "+agent.name+" contribution")
		if committed.Review == nil || committed.Review.Commit == "" || committed.Review.ID != review.Review.ID {
			t.Fatalf("%s approved review has no matching receipt: %+v", agent.name, committed.Review)
		}
		preparedReviews = append(preparedReviews, prepared{commit: committed.Review.Commit, reviewPath: review.Review.Path})
	}
	if got := r.Git("rev-parse", "HEAD"); got != mainHead {
		t.Fatalf("Workcopy advanced the source branch while preparing reviews: %s != %s", got, mainHead)
	}
	if got := r.Git("write-tree"); got != mainIndex {
		t.Fatalf("Workcopy changed the source index while preparing reviews: %s != %s", got, mainIndex)
	}
	coordinator := invoke(t, r.Path, 0, "create", "coordinator", "--root", root, "--all")
	if coordinator.Workspace.Branch != "refs/heads/main" {
		t.Fatalf("coordinator lane did not pin the source main branch: %+v", coordinator.Workspace)
	}
	if projects, err := os.ReadDir(filepath.Join(coordinator.Workspace.Path, "src")); err != nil || len(projects) != 25 {
		t.Fatalf("coordinator lane did not materialize the complete monorepo: %d projects, %v", len(projects), err)
	}
	for _, review := range preparedReviews {
		r.Git("cherry-pick", review.commit)
	}
	coordinatorPath := coordinator.Workspace.Path
	invoke(t, coordinatorPath, 0, "sync")
	coordinatorStatus := invoke(t, coordinatorPath, 0, "status")
	if len(coordinatorStatus.Changes) != 0 || len(coordinatorStatus.Conflicts) != 0 {
		t.Fatalf("coordinator lane did not reconcile the independent agent integrations: %+v", coordinatorStatus)
	}
	invoke(t, coordinatorPath, 0, "destroy")
	for _, agent := range agents {
		lanePath := lanePaths[agent.name]
		invoke(t, lanePath, 0, "sync")
		status := invoke(t, lanePath, 0, "status")
		if len(status.Changes) != 0 || len(status.Conflicts) != 0 {
			t.Fatalf("integrated contribution did not reconcile cleanly for %s: %+v", agent.name, status)
		}
		invoke(t, lanePath, 0, "destroy")
	}
	listed := invoke(t, r.Path, 0, "list", "--root", root)
	if len(listed.Workspaces) != 0 {
		t.Fatalf("agent lanes remain registered after ordinary destroy: %+v", listed.Workspaces)
	}
	for _, agent := range agents {
		body, err := os.ReadFile(filepath.Join(r.Path, filepath.FromSlash(agent.file)))
		if err != nil || !strings.Contains(string(body), "contribution from "+agent.name) {
			t.Fatalf("integrated source missing %s contribution: %v", agent.name, err)
		}
	}
	for _, review := range preparedReviews {
		if _, err := os.Stat(filepath.Join(review.reviewPath, ".git")); err != nil {
			t.Fatalf("separate human review was not retained: %v", err)
		}
	}
}

func TestSelectedGoModuleBuildsAndTestsInsideLane(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("services/api/go.mod", "module example.invalid/workcopy-lane-api\n\ngo 1.23.0\n")
	r.Write("services/api/.gitignore", ".workcopy-build/\n")
	r.Write("services/api/main.go", "package main\n\nfunc add(left, right int) int { return left + right }\n\nfunc main() {}\n")
	r.Write("services/api/main_test.go", "package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if add(2, 3) != 5 { t.Fatal(\"wrong sum\") } }\n")
	r.Commit("add offline Go module")

	root := filepath.Join(r.Parent, "lanes")
	created := invoke(t, r.Path, 0, "create", "go-api", "--root", root, "--project", "services/api")
	lane := created.Workspace.Path
	module := filepath.Join(lane, "services", "api")
	for _, name := range []string{"go.mod", "main.go", "main_test.go"} {
		if _, err := os.Stat(filepath.Join(module, name)); err != nil {
			t.Fatalf("lane is missing build input %s: %v", name, err)
		}
	}

	runGo := func(arguments ...string) {
		t.Helper()
		command := exec.Command("go", arguments...)
		command.Dir = module
		command.Env = offlineGoEnvironment()
		body, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s in lane: %v\n%s", strings.Join(arguments, " "), err, body)
		}
	}
	runGo("test", "./...")

	outputDirectory := filepath.Join(module, ".workcopy-build")
	if err := os.MkdirAll(outputDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(outputDirectory, "api")
	if runtime.GOOS == "windows" {
		output += ".exe"
	}
	runGo("build", "-o", output, ".")
	if info, err := os.Stat(output); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("lane-local build output is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Path, "services", "api", ".workcopy-build")); !os.IsNotExist(err) {
		t.Fatalf("build output escaped the lane into the source checkout: %v", err)
	}
	status := invoke(t, lane, 0, "status")
	if len(status.Changes) != 0 {
		t.Fatalf("ignored lane-local build output appeared as a source change: %+v", status.Changes)
	}
}

func offlineGoEnvironment() []string {
	environment := []string{}
	for _, value := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
		if key != "GOPROXY" && key != "GOSUMDB" && key != "GOWORK" && key != "GOTOOLCHAIN" {
			environment = append(environment, value)
		}
	}
	return append(environment, "GOPROXY=off", "GOSUMDB=off", "GOWORK=off", "GOTOOLCHAIN=local")
}

func TestCLIRejectsUnknownArgumentsAndRoundTripsSpaces(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("a file.txt", "base")
	r.Commit("base")
	invoke(t, r.Path, 2, "status", "--unknown")
	created := invoke(t, r.Path, 0, "create", "example", "--root", filepath.Join(r.Parent, "spaces in path"), "--all")
	os.WriteFile(filepath.Join(created.Workspace.Path, "a file.txt"), []byte("changed"), 0644)
	status := invoke(t, r.Path, 0, "status", "--workspace", created.Workspace.Path)
	if len(status.Changes) != 1 || status.Changes[0].Path != "a file.txt" {
		t.Fatal(status)
	}
	invoke(t, r.Path, 0, "destroy", "--workspace", created.Workspace.Path, "--force")
}
func TestHelpListsEveryDocumentedCommand(t *testing.T) {
	help := invoke(t, t.TempDir(), 0, "help")
	for _, command := range []string{"create", "expand", "status", "sync", "resolve", "merge", "destroy", "list", "discover", "solution", "share", "unshare", "recover", "content", "doctor", "legacy", "version"} {
		if !strings.Contains(help.Message, "workcopy "+command) {
			t.Error("help missing", command)
		}
	}
	createStart := strings.Index(help.Message, "workcopy create NAME")
	if createStart < 0 {
		t.Fatal("help is missing the create usage entry")
	}
	createEnd := strings.Index(help.Message[createStart:], "workcopy expand")
	if createEnd < 0 || !strings.Contains(help.Message[createStart:createStart+createEnd], "--dry-run") {
		t.Error("create help is missing the supported --dry-run option")
	}
}
