// Package project discovers declarative project boundaries without running builds.
package project

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"workcopy/internal/fsops"
	"workcopy/internal/git"
	"workcopy/internal/model"
)

type Catalog struct {
	Projects []model.Project `json:"projects"`
	Warnings []string        `json:"warnings,omitempty"`
}

func Discover(ctx context.Context, repository *git.Repository, tree map[string]git.TreeEntry, config model.Config) (Catalog, error) {
	catalog := Catalog{Projects: append([]model.Project(nil), config.Projects...)}
	enabled := map[string]bool{}
	for _, adapter := range config.Discovery.Adapters {
		switch adapter {
		case "generic", "dotnet", "node", "go", "auto":
			enabled[adapter] = true
		default:
			return catalog, model.Fail(2, "invalid-adapter", "Unknown discovery adapter: "+adapter)
		}
	}
	if enabled["auto"] {
		enabled["dotnet"] = true
		enabled["node"] = true
		enabled["go"] = true
	}
	reader, err := repository.Reader(ctx)
	if err != nil {
		return catalog, err
	}
	defer reader.Close()
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	sort.Strings(names)
	ids := map[string]bool{}
	paths := map[string]string{}
	for _, p := range catalog.Projects {
		if ids[p.ID] {
			return catalog, fmt.Errorf("duplicate project ID: %s", p.ID)
		}
		ids[p.ID] = true
		paths[p.Path] = p.ID
	}
	references := map[string][]string{}
	packageNames := map[string]string{}
	nodeReferences := map[string][]string{}
	for _, name := range names {
		if err = ctx.Err(); err != nil {
			return catalog, err
		}
		ext := path.Ext(name)
		kind := ""
		if enabled["dotnet"] && (ext == ".csproj" || ext == ".fsproj" || ext == ".vbproj") {
			kind = "dotnet"
		}
		if enabled["node"] && path.Base(name) == "package.json" && !strings.Contains("/"+name, "/node_modules/") {
			kind = "node"
		}
		if enabled["go"] && path.Base(name) == "go.mod" {
			kind = "go"
		}
		if kind == "" {
			continue
		}
		if tree[name].Kind != "blob" || tree[name].Mode == 0120000 {
			return catalog, fmt.Errorf("project manifest is not a regular blob: %s", name)
		}
		folder := path.Dir(name)
		id := paths[folder]
		if id == "" {
			id = name
			paths[folder] = id
			if ids[id] {
				return catalog, fmt.Errorf("duplicate project ID: %s", id)
			}
			ids[id] = true
			catalog.Projects = append(catalog.Projects, model.Project{ID: id, Path: folder, Kind: kind})
		}
		body, readErr := reader.Read(tree[name].ObjectID)
		if readErr != nil {
			return catalog, readErr
		}
		switch kind {
		case "dotnet":
			decoder := xml.NewDecoder(bytes.NewReader(body))
			for {
				token, decodeErr := decoder.Token()
				if decodeErr == io.EOF {
					break
				}
				if decodeErr != nil {
					return catalog, fmt.Errorf("%s: %w", name, decodeErr)
				}
				element, ok := token.(xml.StartElement)
				if !ok || element.Name.Local != "ProjectReference" {
					continue
				}
				include := ""
				conditional := false
				for _, attribute := range element.Attr {
					if attribute.Name.Local == "Include" {
						include = attribute.Value
					}
					if attribute.Name.Local == "Condition" {
						conditional = true
					}
				}
				if include == "" {
					continue
				}
				if conditional {
					catalog.Warnings = append(catalog.Warnings, "Conditional reference is conservatively included in "+name)
				}
				if strings.ContainsAny(include, "$%*?;") {
					catalog.Warnings = append(catalog.Warnings, "Unevaluated MSBuild reference in "+name+": "+include)
					continue
				}
				target := path.Clean(path.Join(folder, strings.ReplaceAll(include, "\\", "/")))
				if target == ".." || strings.HasPrefix(target, "../") {
					catalog.Warnings = append(catalog.Warnings, "Project "+name+" requires external source "+include+"; it is not copied into a lane. Use an explicitly configured package reference or isolated dependency input before building this project.")
					continue
				}
				if err = fsops.ValidateSourcePath(target); err != nil {
					return catalog, err
				}
				references[id] = append(references[id], target)
			}
		case "node":
			var manifest struct {
				Name                 string            `json:"name"`
				Dependencies         map[string]string `json:"dependencies"`
				DevDependencies      map[string]string `json:"devDependencies"`
				PeerDependencies     map[string]string `json:"peerDependencies"`
				OptionalDependencies map[string]string `json:"optionalDependencies"`
			}
			if err = json.Unmarshal(body, &manifest); err != nil {
				return catalog, fmt.Errorf("%s: %w", name, err)
			}
			if manifest.Name != "" {
				if previous := packageNames[manifest.Name]; previous != "" && previous != id {
					return catalog, fmt.Errorf("duplicate Node package name: %s", manifest.Name)
				}
				packageNames[manifest.Name] = id
			}
			for _, deps := range []map[string]string{manifest.Dependencies, manifest.DevDependencies, manifest.PeerDependencies, manifest.OptionalDependencies} {
				for dependency := range deps {
					nodeReferences[id] = append(nodeReferences[id], dependency)
				}
			}
		}
	}
	for index := range catalog.Projects {
		p := &catalog.Projects[index]
		for _, target := range references[p.ID] {
			dependency := paths[path.Dir(target)]
			if dependency == "" {
				return catalog, fmt.Errorf("reference %s from %s is not a discovered project; declare it explicitly", target, p.ID)
			}
			p.Dependencies = append(p.Dependencies, dependency)
		}
		for _, name := range nodeReferences[p.ID] {
			if dependency := packageNames[name]; dependency != "" && dependency != p.ID {
				p.Dependencies = append(p.Dependencies, dependency)
			}
		}
		sort.Strings(p.Dependencies)
		p.Dependencies = unique(p.Dependencies)
	}
	sort.Slice(catalog.Projects, func(i, j int) bool { return catalog.Projects[i].ID < catalog.Projects[j].ID })
	sort.Strings(catalog.Warnings)
	return catalog, nil
}
func unique(values []string) []string {
	result := []string{}
	for _, v := range values {
		if len(result) == 0 || v != result[len(result)-1] {
			result = append(result, v)
		}
	}
	return result
}

// Match adds recursive ** path segments to the standard path.Match syntax.
func Match(pattern, name string) bool {
	if pattern == "." || pattern == name || strings.HasPrefix(name, strings.TrimSuffix(pattern, "/")+"/") {
		return true
	}
	p := strings.Split(pattern, "/")
	n := strings.Split(name, "/")
	return matchParts(p, n)
}
func matchParts(pattern, name []string) bool {
	// One row per pattern segment bounds recursive-glob work by the two lengths.
	previous := make([]bool, len(name)+1)
	previous[0] = true
	for _, segment := range pattern {
		current := make([]bool, len(name)+1)
		if segment == "**" {
			current[0] = previous[0]
			for n := 1; n <= len(name); n++ {
				current[n] = previous[n] || current[n-1]
			}
		} else {
			for n := 1; n <= len(name); n++ {
				matched, err := path.Match(segment, name[n-1])
				current[n] = err == nil && matched && previous[n-1]
			}
		}
		previous = current
	}
	return previous[len(name)]
}
func Selected(selection model.Selection, name string) bool {
	included := selection.All || !strings.Contains(name, "/")
	for _, pattern := range append(append([]string(nil), selection.Paths...), selection.Include...) {
		if Match(pattern, name) {
			included = true
		}
	}
	for _, pattern := range selection.Exclude {
		if Match(pattern, name) {
			return false
		}
	}
	return included
}
func Select(config model.Config, catalog Catalog, profileName string, requested []string, all bool) (model.Selection, error) {
	selection := model.Selection{All: all}
	projects := map[string]model.Project{}
	paths := map[string][]string{}
	for _, p := range catalog.Projects {
		if p.ID == "" {
			return selection, fmt.Errorf("project ID is empty")
		}
		if p.Path != "." {
			if err := fsops.ValidateSourcePath(p.Path); err != nil {
				return selection, err
			}
		}
		projects[p.ID] = p
		paths[p.Path] = append(paths[p.Path], p.ID)
	}
	profile := model.Profile{}
	if !all && profileName == "" && len(requested) == 0 {
		profileName = config.DefaultProfile
	}
	if profileName != "" {
		var ok bool
		profile, ok = config.Profiles[profileName]
		if !ok {
			return selection, fmt.Errorf("unknown profile: %s", profileName)
		}
	}
	if !all {
		requested = append(append([]string(nil), profile.Projects...), requested...)
	} else {
		requested = nil
	}
	selection.Include = append([]string(nil), profile.Include...)
	selection.Exclude = append([]string(nil), profile.Exclude...)
	if !all && profileName == "" && len(requested) == 0 {
		selection.All = true
	}
	selected := map[string]bool{}
	queue := append([]string(nil), requested...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if selected[id] {
			continue
		}
		p, ok := projects[id]
		if !ok && id != "." {
			aliases := paths[id]
			if len(aliases) > 1 {
				return selection, model.Fail(2, "ambiguous-project-path", "More than one project uses this directory; select project IDs: "+id)
			}
			if len(aliases) == 1 {
				id = aliases[0]
				p, ok = projects[id]
				if selected[id] {
					continue
				}
			}
		}
		if !ok {
			if err := fsops.ValidateSourcePath(id); err != nil {
				return selection, err
			}
			selection.Paths = append(selection.Paths, id)
			selected[id] = true
			continue
		}
		selected[id] = true
		selection.Projects = append(selection.Projects, id)
		selection.Paths = append(selection.Paths, p.Path)
		if profile.DependencyClosure == "include" {
			queue = append(queue, p.Dependencies...)
		}
	}
	for id := range selected {
		if p, ok := projects[id]; ok {
			for _, dependency := range p.Dependencies {
				if !selected[dependency] {
					return selection, model.Fail(3, "missing-project-dependency", id+" requires "+dependency+"; include it or set dependencyClosure to include")
				}
			}
		}
	}
	if profile.DependencyClosure != "" && profile.DependencyClosure != "strict" && profile.DependencyClosure != "include" {
		return selection, fmt.Errorf("dependencyClosure must be strict or include")
	}
	for _, pattern := range append(append([]string(nil), selection.Include...), selection.Exclude...) {
		if strings.Contains(pattern, "\\") || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "..") {
			return selection, fmt.Errorf("invalid selection pattern: %s", pattern)
		}
		for _, segment := range strings.Split(pattern, "/") {
			if segment != "**" {
				if _, err := path.Match(segment, ""); err != nil {
					return selection, err
				}
			}
		}
	}
	sort.Strings(selection.Projects)
	sort.Strings(selection.Paths)
	return selection, nil
}
