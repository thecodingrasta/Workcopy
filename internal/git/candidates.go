package git

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"workcopy/internal/model"
)

func (r *Repository) Candidates(ctx context.Context, lane string, excludedPaths ...string) ([]string, error) {
	// An absent private index makes every lane file untracked for enumeration.
	// Reusing the source index could expose ignored files staged only in main.
	id, err := model.ID()
	if err != nil {
		return nil, err
	}
	index := filepath.Join(lane, ".workcopy", "unused-index-"+id)
	arguments := []string{"--git-dir=" + r.CommonDir, "--work-tree=" + lane, "ls-files", "-z", "--others", "--exclude-standard", "--exclude=.workcopy/"}
	// Exclude mappings before Git walks the tree, including Windows junctions.
	// Escape glob syntax so a literal directory name cannot hide adjacent source.
	escape := strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[", "]", "\\]")
	for _, excluded := range excludedPaths {
		arguments = append(arguments, "--exclude=/"+escape.Replace(excluded))
	}
	body, err := r.RunIndex(ctx, r.Root, index, nil, arguments...)
	if err != nil {
		return nil, err
	}
	result := []string{}
	for _, item := range bytes.Split(body, []byte{0}) {
		if len(item) > 0 {
			result = append(result, strings.TrimSuffix(string(item), "/"))
			if len(result) > 100000 {
				return nil, fmt.Errorf("lane exceeds 100,000 candidate source paths")
			}
		}
	}
	return result, nil
}
