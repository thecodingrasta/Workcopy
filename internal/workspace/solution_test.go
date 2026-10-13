package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"workcopy/internal/testutil"
)

func TestSolutionFilterContainsOnlyMaterializedProjects(t *testing.T) {
	r := testutil.NewRepository(t)
	r.Write("A/A.csproj", `<Project/>`)
	r.Write("B/B.csproj", `<Project/>`)
	r.Write("Repo.sln", `Microsoft Visual Studio Solution File, Format Version 12.00
Project("{A}") = "A", "A\A.csproj", "{B}"
EndProject
Project("{A}") = "B", "B\B.csproj", "{C}"
EndProject
`)
	r.Commit("solution")
	e, _ := New(context.Background(), r.Path, filepath.Join(r.Parent, "lanes"))
	created, err := e.Create(context.Background(), "task", CreateOptions{Projects: []string{"A/A.csproj"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	report, err := e.Solution(context.Background(), "task", "Repo.sln", false)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(created.Workspace.Path, filepath.FromSlash(report.Plan.Files[0])))
	if err != nil {
		t.Fatal(err)
	}
	var filter struct {
		Solution struct {
			Path     string   `json:"path"`
			Projects []string `json:"projects"`
		} `json:"solution"`
	}
	if err = json.Unmarshal(body, &filter); err != nil {
		t.Fatal(err)
	}
	if len(filter.Solution.Projects) != 1 || filter.Solution.Projects[0] != `A\A.csproj` || filter.Solution.Path != "../../Repo.sln" {
		t.Fatalf("bad filter: %+v", filter)
	}
	status, err := e.Status(context.Background(), "task")
	if err != nil || len(status.Changes) != 0 {
		t.Fatal("generated filter entered source changes", status, err)
	}
}
