package workspace

import (
	"context"
	"workcopy/internal/project"
)

func (e *Engine) DiscoverProjects(ctx context.Context, configPath string) (Report, error) {
	return e.discoverProjects(ctx, configPath, nil)
}

// DiscoverProjectsFromConfig uses pinned owner configuration bytes without
// rereading a mutable file. Parsing and discovery share the normal CLI path.
func (e *Engine) DiscoverProjectsFromConfig(ctx context.Context, data []byte) (Report, error) {
	return e.discoverProjects(ctx, "", data)
}

func (e *Engine) discoverProjects(ctx context.Context, configPath string, data []byte) (Report, error) {
	report := Report{Command: "discover"}
	_, commit, err := e.Repository.Resolve(ctx, "")
	if err != nil {
		return report, err
	}
	tree, err := e.Repository.Tree(ctx, commit)
	if err != nil {
		return report, err
	}
	config, err := e.configuration(ctx, tree, configPath, data)
	if err != nil {
		return report, err
	}
	catalog, err := project.Discover(ctx, e.Repository, tree, config)
	if err != nil {
		return report, err
	}
	report.Catalog = &catalog
	report.Config = &config
	report.Message = "Projects were discovered from a committed snapshot; no repository scripts or build tools were executed."
	return report, nil
}
