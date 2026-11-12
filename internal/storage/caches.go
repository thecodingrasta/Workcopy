package storage

import (
	"path/filepath"
)

// CacheLocation is where a build tool keeps a per-user cache. Workcopy never
// reads tool configuration files or runs the tool, so the path is the
// environment variable's value or the tool's documented default.
type CacheLocation struct {
	Tool   string `json:"tool"`
	Path   string `json:"path"`
	Source string `json:"source"`
}

// Environment looks up one variable, like os.LookupEnv.
type Environment func(string) (string, bool)

// ToolCaches names the NuGet and Go caches this user's tools would use. Settings
// that live in files (NuGet.Config, `go env -w`) are not read, and the Source
// text says so, because a wrong location here would mislead the volume check.
func ToolCaches(lookup Environment, home, userCache string) []CacheLocation {
	value := func(name string) string {
		if v, ok := lookup(name); ok {
			return v
		}
		return ""
	}
	const defaultNote = "default location; NuGet.Config and `go env -w` overrides are not read"
	caches := []CacheLocation{}
	if v := value("NUGET_PACKAGES"); v != "" {
		caches = append(caches, CacheLocation{"nuget packages", v, "NUGET_PACKAGES"})
	} else if home != "" {
		caches = append(caches, CacheLocation{"nuget packages", filepath.Join(home, ".nuget", "packages"), defaultNote})
	}
	if v := value("GOCACHE"); v != "" {
		caches = append(caches, CacheLocation{"go build cache", v, "GOCACHE"})
	} else if userCache != "" {
		caches = append(caches, CacheLocation{"go build cache", filepath.Join(userCache, "go-build"), defaultNote})
	}
	if v := value("GOMODCACHE"); v != "" {
		caches = append(caches, CacheLocation{"go module cache", v, "GOMODCACHE"})
	} else if gopath := firstPath(value("GOPATH")); gopath != "" {
		caches = append(caches, CacheLocation{"go module cache", filepath.Join(gopath, "pkg", "mod"), "GOPATH"})
	} else if home != "" {
		caches = append(caches, CacheLocation{"go module cache", filepath.Join(home, "go", "pkg", "mod"), defaultNote})
	}
	return caches
}

// firstPath returns the first entry of a PATH-style list.
func firstPath(list string) string {
	for _, part := range filepath.SplitList(list) {
		if part != "" {
			return part
		}
	}
	return ""
}
