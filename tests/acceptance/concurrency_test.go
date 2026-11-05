package acceptance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"workcopy/internal/testutil"
	"workcopy/internal/workspace"
)

// retryBusy runs a mutation, retrying while another Workcopy process holds the
// writer lock. Any other failure is returned as-is: contention must never turn
// into corruption or an unrelated error.
func retryBusy(t *testing.T, directory string, arguments ...string) (string, int) {
	t.Helper()
	for attempt := 0; attempt < 400; attempt++ {
		out, _, code := run(t, directory, nil, arguments...)
		if code == 3 && strings.Contains(out, "workspace-busy") {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return out, code
	}
	t.Fatalf("still busy after many attempts: %v", arguments)
	return "", 0
}

// Several independent processes, one lane each, sharing one root and one
// repository: every lane ends with exactly its own change, and the registry is
// still consistent afterwards.
func TestConcurrentProcessesKeepLanesIndependent(t *testing.T) {
	const lanes = 6
	r := testutil.NewRepository(t)
	for n := 0; n < lanes; n++ {
		r.Write(fmt.Sprintf("pkg%d/file.txt", n), "base\n")
	}
	for n := 0; n < 40; n++ {
		r.Write(fmt.Sprintf("shared/f%02d.txt", n), "shared\n")
	}
	r.Commit("base")
	root := filepath.Join(r.Parent, "lanes")

	var group sync.WaitGroup
	results := make([]string, lanes)
	failures := make(chan string, lanes*8)
	for n := 0; n < lanes; n++ {
		group.Add(1)
		go func(n int) {
			defer group.Done()
			name := fmt.Sprintf("w%d", n)
			project := fmt.Sprintf("pkg%d", n)
			out, code := retryBusy(t, r.Path, "create", name, "--project", project, "--root", root, "--json")
			if code != 0 {
				failures <- fmt.Sprintf("%s: create exit %d %s", name, code, out)
				return
			}
			var created workspace.Report
			if json.Unmarshal([]byte(out), &created) != nil {
				failures <- name + ": create output " + out
				return
			}
			lane := created.Workspace.Path
			if err := os.WriteFile(filepath.Join(lane, project, "file.txt"), []byte("changed by "+name+"\n"), 0644); err != nil {
				failures <- name + ": edit " + err.Error()
				return
			}
			for i := 0; i < 3; i++ {
				if out, _, code := run(t, lane, nil, "status", "--json"); code != 0 || !strings.Contains(out, project+"/file.txt") {
					failures <- fmt.Sprintf("%s: status during contention exit %d %s", name, code, out)
					return
				}
			}
			out, code = retryBusy(t, lane, "merge", "--json")
			var merged workspace.Report
			if code != 0 || json.Unmarshal([]byte(out), &merged) != nil || merged.Review == nil {
				failures <- fmt.Sprintf("%s: merge exit %d %s", name, code, out)
				return
			}
			results[n] = merged.Review.Path
		}(n)
	}
	group.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if t.Failed() {
		return
	}
	for n, review := range results {
		project := fmt.Sprintf("pkg%d", n)
		status := strings.TrimSpace(r.GitAt(review, "", "status", "--porcelain"))
		if status != "M "+project+"/file.txt" {
			t.Errorf("lane %d review should contain only its own change, got %q", n, status)
		}
		want := fmt.Sprintf("changed by w%d\n", n)
		if body, _ := os.ReadFile(filepath.Join(review, project, "file.txt")); string(body) != want {
			t.Errorf("lane %d review content: %q", n, body)
		}
	}
	out, _, code := run(t, r.Path, nil, "list", "--root", root, "--json")
	var listed workspace.Report
	if code != 0 || json.Unmarshal([]byte(out), &listed) != nil || len(listed.Workspaces) != lanes {
		t.Fatalf("registry after contention: %d %s", code, out)
	}
	for _, item := range listed.Workspaces {
		if item.Phase != "ready" {
			t.Errorf("lane %s left in phase %s", item.Name, item.Phase)
		}
		if _, _, code := run(t, r.Path, nil, "recover", item.Name, "--root", root, "--json"); code != 4 {
			t.Errorf("lane %s should have nothing to recover, exit %d", item.Name, code)
		}
	}
}
