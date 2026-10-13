package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
)

var solutionProject = regexp.MustCompile(`(?mi)^Project\("[^"]+"\)\s*=\s*"[^"]*",\s*"([^"]+\.(?:cs|fs|vb)proj)",`)

func (e *Engine) Solution(ctx context.Context, name, solution string, dryRun bool) (Report, error) {
	report := Report{Command: "solution"}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := fsops.ValidateSourcePath(solution); err != nil {
		return report, err
	}
	if strings.ToLower(path.Ext(solution)) != ".sln" {
		return report, model.Fail(2, "solution-format", "Solution filters currently accept .sln files")
	}
	opened, err := e.openLane(name)
	if err != nil {
		return report, err
	}
	defer opened.close()
	if !dryRun {
		lock, lockErr := e.reloadLocked(opened)
		if lockErr != nil {
			return report, lockErr
		}
		defer lock.Close()
	}
	if _, exists := opened.snapshot.Baseline.Files[solution]; !exists {
		return report, model.Fail(3, "solution-not-selected", "The solution must be included in the lane's tracked selection")
	}
	body, err := opened.root.ReadFile(solution)
	if err != nil {
		return report, err
	}
	projects := []string{}
	for _, match := range solutionProject.FindAllSubmatch(body, -1) {
		solutionPath := string(match[1])
		relative := strings.ReplaceAll(solutionPath, "\\", "/")
		full := path.Clean(path.Join(path.Dir(solution), relative))
		if err = fsops.ValidateSourcePath(full); err != nil {
			return report, err
		}
		if baseline, selected := opened.snapshot.Baseline.Files[full]; selected && baseline.Exists() {
			info, statErr := opened.root.Lstat(full)
			if statErr != nil {
				return report, statErr
			}
			if !info.Mode().IsRegular() {
				return report, fmt.Errorf("solution project is not a regular file: %s", full)
			}
			projects = append(projects, solutionPath)
		}
	}
	if len(projects) == 0 {
		return report, model.Fail(3, "solution-projects-not-found", "No materialized projects from this solution were found")
	}
	sort.Strings(projects)
	filter := struct {
		Solution struct {
			Path     string   `json:"path"`
			Projects []string `json:"projects"`
		} `json:"solution"`
	}{}
	filter.Solution.Path = "../../" + solution
	filter.Solution.Projects = projects
	encoded, err := json.MarshalIndent(filter, "", "  ")
	if err != nil {
		return report, err
	}
	encoded = append(encoded, '\n')
	filename := ".workcopy/views/selection-" + model.Hash(encoded)[:16] + ".slnf"
	report.Plan = &Plan{Action: "solution", Files: []string{filename}, Effects: []string{"Generate a solution filter without rewriting the source solution"}}
	report.Workspace = view(opened.snapshot)
	if dryRun {
		report.Message = "Solution filter planned; no file was created."
		return report, nil
	}
	if existing, readErr := opened.root.ReadFile(filename); readErr == nil {
		if string(existing) != string(encoded) {
			return report, model.Fail(3, "generated-file-changed", "Existing generated filter differs; it was not overwritten")
		}
	} else if os.IsNotExist(readErr) {
		if err = opened.root.WriteAtomic(filename, encoded, 0600); err != nil {
			return report, err
		}
	} else {
		return report, readErr
	}
	report.Message = "Solution filter: " + path.Join(opened.root.Path, filename) + "\nNo build was run; build-system imports and conditions are not evaluated."
	return report, nil
}
