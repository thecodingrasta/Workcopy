package acceptance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"workcopy/internal/testutil"
)

// TestSelectedDotNetProjectBuildsAndTestsInsideLane is opt-in because it runs
// a NuGet restore. Its fixture and package caches are confined to t.TempDir.
func TestSelectedDotNetProjectBuildsAndTestsInsideLane(t *testing.T) {
	if os.Getenv("WORKCOPY_DOTNET_LANE_ACCEPTANCE") != "1" {
		t.Skip("set WORKCOPY_DOTNET_LANE_ACCEPTANCE=1 to run the .NET restore/build/test acceptance")
	}
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Fatal("WORKCOPY_DOTNET_LANE_ACCEPTANCE=1 requires a .NET SDK on PATH")
	}

	repository := testutil.NewRepository(t)
	repository.Write(".gitignore", ".workcopy-build/\n")
	repository.Write(".workcopy.json", `{"schemaVersion":1,"defaultProfile":"daily","discovery":{"adapters":["dotnet"]},"projects":[{"id":"contracts","path":"src/Contracts"},{"id":"api","path":"src/Api","dependencies":["contracts"]},{"id":"api-tests","path":"tests/Api.Tests","dependencies":["api"]}],"profiles":{"daily":{"include":["global.json","Directory.Build.props","Directory.Packages.props","Monorepo.sln",".gitignore"],"projects":["api-tests"],"dependencyClosure":"include"}}}`)
	repository.Write("global.json", `{"sdk":{"version":"8.0.100","rollForward":"latestMajor","allowPrerelease":false}}`)
	repository.Write("Directory.Build.props", `<Project>
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
    <Nullable>enable</Nullable>
    <ImplicitUsings>enable</ImplicitUsings>
    <BaseIntermediateOutputPath>$(MSBuildThisFileDirectory).workcopy-build/obj/$(MSBuildProjectName)/</BaseIntermediateOutputPath>
    <BaseOutputPath>$(MSBuildThisFileDirectory).workcopy-build/bin/$(MSBuildProjectName)/</BaseOutputPath>
  </PropertyGroup>
</Project>`)
	repository.Write("Directory.Packages.props", `<Project>
  <PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup>
  <ItemGroup>
    <PackageVersion Include="Microsoft.NET.Test.Sdk" Version="17.12.0" />
    <PackageVersion Include="xunit" Version="2.9.2" />
    <PackageVersion Include="xunit.runner.visualstudio" Version="2.8.2" />
  </ItemGroup>
</Project>`)
	repository.Write("src/Contracts/Contracts.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Library</OutputType></PropertyGroup></Project>`)
	repository.Write("src/Contracts/Maths.cs", `namespace Contracts;
public static class Maths { public static int Add(int left, int right) => left + right; }`)
	repository.Write("src/Api/Api.csproj", `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><ProjectReference Include="../Contracts/Contracts.csproj" /></ItemGroup></Project>`)
	repository.Write("src/Api/Api.cs", `using Contracts;
namespace Api;
public static class Calculator { public static int Add(int left, int right) => Maths.Add(left, right); }`)
	repository.Write("tests/Api.Tests/Api.Tests.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><IsPackable>false</IsPackable><IsTestProject>true</IsTestProject></PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Microsoft.NET.Test.Sdk" />
    <PackageReference Include="xunit" />
    <PackageReference Include="xunit.runner.visualstudio"><PrivateAssets>all</PrivateAssets></PackageReference>
  </ItemGroup>
  <ItemGroup><ProjectReference Include="../../src/Api/Api.csproj" /></ItemGroup>
</Project>`)
	repository.Write("tests/Api.Tests/CalculatorTests.cs", `using Api;
using Xunit;
namespace Api.Tests;
public class CalculatorTests { [Fact] public void AddCombinesValues() => Assert.Equal(5, Calculator.Add(2, 3)); }`)
	repository.Write("Monorepo.sln", dotnetSolution)
	repository.Commit("dotnet lane fixture")
	headBeforeBuild := repository.Git("rev-parse", "HEAD")

	created := invoke(t, repository.Path, 0, "create", "dotnet-api", "--root", filepath.Join(repository.Parent, "lanes"), "--profile", "daily")
	lane := created.Workspace.Path
	if _, err := os.Lstat(filepath.Join(lane, ".git")); !os.IsNotExist(err) {
		t.Fatalf("selected lane must remain Git-free: %v", err)
	}
	filterReport := invoke(t, lane, 0, "solution", "--solution", "Monorepo.sln")
	if filterReport.Plan == nil || len(filterReport.Plan.Files) != 1 {
		t.Fatalf("solution filter path missing from report: %+v", filterReport.Plan)
	}
	filter := filepath.Join(lane, filepath.FromSlash(filterReport.Plan.Files[0]))
	nugetPackages := filepath.Join(repository.Parent, "nuget-packages")
	if configured := os.Getenv("WORKCOPY_DOTNET_NUGET_PACKAGES"); configured != "" {
		nugetPackages = configured
	}

	if output := runDotnet(t, lane, repository.Parent, "--version"); strings.TrimSpace(output) == "" {
		t.Fatal("dotnet did not report an SDK version")
	}
	runDotnet(t, lane, repository.Parent, "restore", filter, "--packages", nugetPackages, "--source", "https://api.nuget.org/v3/index.json", "-p:NuGetAudit=false", "--disable-build-servers", "--disable-parallel", "-p:UseSharedCompilation=false", "-nodeReuse:false", "--nologo")
	runDotnet(t, lane, repository.Parent, "build", filter, "--no-restore", "--configuration", "Debug", "--disable-build-servers", "-p:UseSharedCompilation=false", "-nodeReuse:false", "-m:1", "--nologo")
	testOutput := runDotnet(t, lane, repository.Parent, "test", filter, "--no-build", "--no-restore", "--configuration", "Debug", "--disable-build-servers", "-p:UseSharedCompilation=false", "-nodeReuse:false", "--nologo")
	if !regexp.MustCompile(`Passed!.*Total:\s*1`).MatchString(testOutput) {
		t.Fatalf("dotnet test did not report one executed passing test:\n%s", testOutput)
	}
	if status := invoke(t, lane, 0, "status"); len(status.Changes) != 0 {
		t.Fatalf("lane-local build output appeared as source changes: %+v", status.Changes)
	}
	if got := repository.Git("rev-parse", "HEAD"); got != headBeforeBuild {
		t.Fatalf("source checkout HEAD changed: got %s, want %s", got, headBeforeBuild)
	}
	if status := repository.Git("status", "--porcelain=v1"); status != "" {
		t.Fatalf("build modified the source checkout: %s", status)
	}
	if err := filepath.WalkDir(repository.Path, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() && (entry.Name() == "bin" || entry.Name() == "obj" || entry.Name() == ".workcopy-build") {
			return fmt.Errorf("build output escaped lane: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lane, ".workcopy-build", "bin", "Api.Tests", "Debug", "net8.0", "Api.Tests.dll")); err != nil {
		t.Fatalf("test output is not lane-local: %v", err)
	}
}

func runDotnet(t *testing.T, directory, cacheRoot string, arguments ...string) string {
	t.Helper()
	command := exec.Command("dotnet", arguments...)
	command.Dir = directory
	command.Env = dotnetEnvironment(cacheRoot)
	body, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dotnet %s in lane: %v\n%s", strings.Join(arguments, " "), err, body)
	}
	return string(body)
}

func dotnetEnvironment(cacheRoot string) []string {
	blocked := map[string]bool{
		"APPDATA": true, "LOCALAPPDATA": true, "NUGET_HTTP_CACHE_PATH": true,
		"NUGET_PACKAGES": true, "DOTNET_CLI_HOME": true, "DOTNET_CLI_TELEMETRY_OPTOUT": true,
		"DOTNET_SKIP_FIRST_TIME_EXPERIENCE": true, "TEMP": true, "TMP": true,
		"DOTNET_GENERATE_ASPNET_CERTIFICATE": true,
		"WORKCOPY_DOTNET_NUGET_PACKAGES":     true,
		"MSBUILDDISABLENODEREUSE":            true,
		"DOTNET_CLI_USE_MSBUILD_SERVER":      true,
		"USESHAREDCOMPILATION":               true,
	}
	environment := make([]string, 0, len(os.Environ())+7)
	for _, value := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
		if !blocked[key] {
			environment = append(environment, value)
		}
	}
	return append(environment,
		"APPDATA="+filepath.Join(cacheRoot, "appdata"),
		"LOCALAPPDATA="+filepath.Join(cacheRoot, "localappdata"),
		"NUGET_HTTP_CACHE_PATH="+filepath.Join(cacheRoot, "nuget-http-cache"),
		"NUGET_PACKAGES="+nugetPackagesFor(cacheRoot),
		"DOTNET_CLI_HOME="+filepath.Join(cacheRoot, "dotnet-cli"),
		"DOTNET_CLI_TELEMETRY_OPTOUT=1",
		"DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1",
		"DOTNET_GENERATE_ASPNET_CERTIFICATE=false",
		"MSBUILDDISABLENODEREUSE=1",
		"DOTNET_CLI_USE_MSBUILD_SERVER=0",
		"UseSharedCompilation=false",
		"TEMP="+cacheRoot,
		"TMP="+cacheRoot,
	)
}

func nugetPackagesFor(cacheRoot string) string {
	if configured := os.Getenv("WORKCOPY_DOTNET_NUGET_PACKAGES"); configured != "" {
		return configured
	}
	return filepath.Join(cacheRoot, "nuget-packages")
}

func TestDotnetFixtureDisablesPersistentBuildServers(t *testing.T) {
	t.Setenv("MSBUILDDISABLENODEREUSE", "0")
	t.Setenv("DOTNET_CLI_USE_MSBUILD_SERVER", "1")
	t.Setenv("UseSharedCompilation", "true")
	environment := dotnetEnvironment(t.TempDir())
	for key, wanted := range map[string]string{"MSBUILDDISABLENODEREUSE": "1", "DOTNET_CLI_USE_MSBUILD_SERVER": "0", "USESHAREDCOMPILATION": "false"} {
		values := []string{}
		for _, entry := range environment {
			parts := strings.SplitN(entry, "=", 2)
			if strings.ToUpper(parts[0]) == key {
				values = append(values, parts[1])
			}
		}
		if len(values) != 1 || values[0] != wanted {
			t.Errorf("%s: got %v, want exactly [%s]", key, values, wanted)
		}
	}
}

const dotnetSolution = `Microsoft Visual Studio Solution File, Format Version 12.00
# Visual Studio Version 17
VisualStudioVersion = 17.0.31903.59
MinimumVisualStudioVersion = 10.0.40219.1
Project("{FAE04EC0-301F-11D3-BF4B-00C04F79EFBC}") = "Contracts", "src\Contracts\Contracts.csproj", "{11111111-1111-4111-8111-111111111111}"
EndProject
Project("{FAE04EC0-301F-11D3-BF4B-00C04F79EFBC}") = "Api", "src\Api\Api.csproj", "{22222222-2222-4222-8222-222222222222}"
EndProject
Project("{FAE04EC0-301F-11D3-BF4B-00C04F79EFBC}") = "Api.Tests", "tests\Api.Tests\Api.Tests.csproj", "{33333333-3333-4333-8333-333333333333}"
EndProject
Global
  GlobalSection(SolutionConfigurationPlatforms) = preSolution
    Debug|Any CPU = Debug|Any CPU
  EndGlobalSection
  GlobalSection(ProjectConfigurationPlatforms) = postSolution
    {11111111-1111-4111-8111-111111111111}.Debug|Any CPU.ActiveCfg = Debug|Any CPU
    {11111111-1111-4111-8111-111111111111}.Debug|Any CPU.Build.0 = Debug|Any CPU
    {22222222-2222-4222-8222-222222222222}.Debug|Any CPU.ActiveCfg = Debug|Any CPU
    {22222222-2222-4222-8222-222222222222}.Debug|Any CPU.Build.0 = Debug|Any CPU
    {33333333-3333-4333-8333-333333333333}.Debug|Any CPU.ActiveCfg = Debug|Any CPU
    {33333333-3333-4333-8333-333333333333}.Debug|Any CPU.Build.0 = Debug|Any CPU
  EndGlobalSection
EndGlobal
`
