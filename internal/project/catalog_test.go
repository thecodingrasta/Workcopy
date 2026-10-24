package project

import (
	"context"
	"strings"
	"testing"
	"time"

	"workcopy/internal/git"
	"workcopy/internal/model"
	"workcopy/internal/testutil"
)

func TestDotnetDiscoveryFindsLiteralReferences(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("A/A.csproj", `<Project><ItemGroup><ProjectReference Include="..\B\B.csproj"/></ItemGroup></Project>`)
	r.Write("B/B.csproj", `<Project/>`)
	commit := r.Commit("projects")
	repo, _ := git.Open(context.Background(), r.Path)
	tree, _ := repo.Tree(context.Background(), commit)
	catalog, err := Discover(context.Background(), repo, tree, model.Config{Discovery: model.Discovery{Adapters: []string{"dotnet"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Projects) != 2 || len(catalog.Projects[0].Dependencies) != 1 || catalog.Projects[0].Dependencies[0] != "B/B.csproj" {
		t.Fatalf("wrong catalog: %+v", catalog)
	}
}

func TestExternalDotnetReferenceDoesNotBlockIndependentProjects(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("Domain/Domain.csproj", `<Project/>`)
	r.Write("Server/Server.csproj", `<Project><ItemGroup><ProjectReference Include="../../Sibling/Core.csproj"/></ItemGroup></Project>`)
	commit := r.Commit("external dependency")
	repo, err := git.Open(context.Background(), r.Path)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.Tree(context.Background(), commit)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Discover(context.Background(), repo, tree, model.Config{Discovery: model.Discovery{Adapters: []string{"dotnet"}}})
	if err != nil {
		t.Fatalf("independent project discovery blocked: %v", err)
	}
	if len(catalog.Projects) != 2 || len(catalog.Warnings) != 1 || !strings.Contains(catalog.Warnings[0], "external") {
		t.Fatalf("external prerequisite not surfaced: %+v", catalog)
	}
}

func TestSelectingKnownProjectPathEnforcesDependencies(t *testing.T) {
	config := model.Config{Projects: []model.Project{{ID: "api", Path: "src/api", Dependencies: []string{"core"}}, {ID: "core", Path: "src/core"}}}
	_, err := Select(config, Catalog{Projects: config.Projects}, "", []string{"src/api"}, false)
	if err == nil {
		t.Fatal("project directory bypassed dependency closure")
	}
	selection, err := Select(config, Catalog{Projects: config.Projects}, "", []string{"src/api", "src/core"}, false)
	if err != nil || len(selection.Projects) != 2 {
		t.Fatalf("path aliases did not select project identities: %+v %v", selection, err)
	}
}

func TestNodeDiscoveryFindsLocalPackageDependenciesAndIgnoresNodeModules(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("apps/web/package.json", `{"name":"@fixture/web","dependencies":{"@fixture/shared":"workspace:*"}}`)
	r.Write("packages/shared/package.json", `{"name":"@fixture/shared"}`)
	r.Write("node_modules/installed/package.json", `{"name":"@fixture/installed"}`)
	commit := r.Commit("Node package fixture")
	repo, err := git.Open(context.Background(), r.Path)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.Tree(context.Background(), commit)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Discover(context.Background(), repo, tree, model.Config{Discovery: model.Discovery{Adapters: []string{"node"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Projects) != 2 {
		t.Fatalf("unexpected projects, including installed dependencies: %+v", catalog.Projects)
	}
	if len(catalog.Projects[0].Dependencies) != 1 || catalog.Projects[0].Dependencies[0] != "packages/shared/package.json" {
		t.Fatalf("local Node dependency was not discovered: %+v", catalog.Projects)
	}
}

func TestNodeDiscoveryRejectsDuplicateLocalPackageNames(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("packages/first/package.json", `{"name":"@fixture/shared"}`)
	r.Write("packages/second/package.json", `{"name":"@fixture/shared"}`)
	commit := r.Commit("duplicate Node package fixture")
	repo, err := git.Open(context.Background(), r.Path)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.Tree(context.Background(), commit)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Discover(context.Background(), repo, tree, model.Config{Discovery: model.Discovery{Adapters: []string{"node"}}})
	if err == nil || !strings.Contains(err.Error(), "duplicate Node package name") {
		t.Fatalf("duplicate Node package name error = %v", err)
	}
}

func TestGlobMatchesNestedAndRootFiles(t *testing.T) {
	for _, name := range []string{"README.md", "docs/README.md", "docs/nested/README.md"} {
		if !Match("**/*.md", name) {
			t.Fatal(name)
		}
	}
	if Match("*.md", "docs/README.md") {
		t.Fatal("star crossed separator")
	}
}

func TestRecursiveGlobDoesNotExpandExponentially(t *testing.T) {
	done := make(chan bool, 1)
	go func() { done <- Match(strings.Repeat("**/", 32)+"end", strings.Repeat("a/", 40)+"different") }()
	select {
	case matched := <-done:
		if matched {
			t.Fatal("incorrect match")
		}
	case <-time.After(time.Second):
		t.Fatal("recursive glob did not finish within a bounded interval")
	}
}

func TestDotnetDiscoveryResolvesCaseOnlyProjectReferences(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("Tools/Compiler/Compiler.csproj", `<Project><ItemGroup><ProjectReference Include="..\..\resources\Core\Core.csproj" /></ItemGroup></Project>`)
	r.Write("Resources/Core/Core.csproj", `<Project/>`)
	commit := r.Commit("case-only project reference")
	repo, err := git.Open(context.Background(), r.Path)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.Tree(context.Background(), commit)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Discover(context.Background(), repo, tree, model.Config{Discovery: model.Discovery{Adapters: []string{"dotnet"}}})
	if err != nil {
		t.Fatal(err)
	}
	var compiler model.Project
	for _, candidate := range catalog.Projects {
		if candidate.Path == "Tools/Compiler" {
			compiler = candidate
		}
	}
	if len(compiler.Dependencies) != 1 || compiler.Dependencies[0] != "Resources/Core/Core.csproj" {
		t.Fatalf("case-only project reference was not resolved: %+v", compiler)
	}
	if len(catalog.Warnings) != 1 || !strings.Contains(catalog.Warnings[0], "differs only by path case") {
		t.Fatalf("case-only warning not surfaced: %+v", catalog.Warnings)
	}
}

func TestUnresolvedDotnetReferenceWarnsWithoutBlockingIndependentProjects(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("Broken/Broken.csproj", `<Project><ItemGroup><ProjectReference Include="../Missing/Missing.csproj" /></ItemGroup></Project>`)
	r.Write("Independent/Independent.csproj", `<Project/>`)
	commit := r.Commit("unresolved internal reference")
	repo, err := git.Open(context.Background(), r.Path)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.Tree(context.Background(), commit)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Discover(context.Background(), repo, tree, model.Config{Discovery: model.Discovery{Adapters: []string{"dotnet"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Projects) != 2 || len(catalog.Warnings) != 1 || !strings.Contains(catalog.Warnings[0], "not a discovered project") {
		t.Fatalf("unresolved reference did not remain an actionable warning: %+v", catalog)
	}
}
