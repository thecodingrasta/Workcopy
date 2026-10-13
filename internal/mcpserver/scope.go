package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"workcopy/internal/model"
	"workcopy/internal/workspace"
)

func directoryScope(directory string, scope Scope) (Scope, error) {
	if !filepath.IsAbs(directory) || scope.Repo != "" || scope.Root != "" {
		return Scope{}, model.Fail(2, "invalid-directory-scope", "Pass an absolute directory alone, or use repo/root explicitly")
	}
	manifest, err := workspace.Locate(directory)
	if err == nil {
		return Scope{Repo: manifest.RepositoryRoot, Root: manifest.WorkRoot}, nil
	}
	var failure *model.Failure
	if errors.As(err, &failure) && failure.Code == "not-in-workspace" {
		return Scope{Repo: directory}, nil
	}
	return Scope{}, err
}

func (s *Server) selectScope(ctx context.Context, scope Scope, optional bool) (*Server, error) {
	if scope.Repo == "" && scope.Root == "" && s.engine != nil {
		return s, nil
	}
	repo := scope.Repo
	if repo == "" && s.engine != nil {
		repo = s.engine.Repository.Root
	}
	if repo == "" {
		if optional && scope.Root == "" {
			return s, nil
		}
		return nil, model.Fail(2, "repository-required", "Pass repo as the absolute checkout path to workcopy_bootstrap, then retain the returned repo/root scope for lane calls")
	}
	if !filepath.IsAbs(repo) || (scope.Root != "" && !filepath.IsAbs(scope.Root)) {
		return nil, model.Fail(2, "absolute-scope-required", "repo and root must be absolute local paths")
	}
	engine, err := workspace.New(ctx, repo, scope.Root)
	if err != nil {
		return nil, err
	}
	sameDefault := false
	if s.engine != nil {
		selectedInfo, err := os.Stat(engine.Repository.Root)
		if err != nil {
			return nil, err
		}
		defaultInfo, err := os.Stat(s.engine.Repository.Root)
		if err != nil {
			return nil, err
		}
		sameDefault = os.SameFile(selectedInfo, defaultInfo)
	}
	if scope.Root == "" {
		root := ""
		if sameDefault {
			root = s.engine.WorkRoot
		} else if s.opts.Root != "" {
			parent := s.opts.Root
			if s.engine != nil {
				parent = filepath.Dir(s.engine.WorkRoot)
			}
			identity := engine.Repository.CommonDir + "\x00" + engine.Repository.Root
			root = filepath.Join(parent, filepath.Base(engine.Repository.Root)+"-"+model.Hash([]byte(identity))[:12])
		}
		if root != "" {
			engine, err = workspace.New(ctx, repo, root)
			if err != nil {
				return nil, err
			}
		}
	}
	selected := &Server{engine: engine, opts: s.opts}
	if sameDefault {
		selected.configData, selected.configErr = s.configData, s.configErr
	}
	return selected, nil
}

func rootKey(root string) string {
	root = filepath.Clean(root)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	return root
}

func (s *Server) beginMutation(root string) bool {
	s.writer.Lock()
	defer s.writer.Unlock()
	key := rootKey(root)
	if s.activeRoots[key] {
		return false
	}
	if s.activeRoots == nil {
		s.activeRoots = make(map[string]bool)
	}
	s.activeRoots[key] = true
	return true
}

func (s *Server) endMutation(root string) {
	s.writer.Lock()
	defer s.writer.Unlock()
	delete(s.activeRoots, rootKey(root))
}
