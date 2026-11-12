package workspace

import (
	"context"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"workcopy/internal/storage"
)

// Storage classes. A file belongs to the first class that claims it, in this
// order, so tracked source is never reported as build output.
const (
	classMetadata       storage.Class = "workcopy-metadata"
	classSource         storage.Class = "source"
	classAgentContext   storage.Class = "agent-context"
	classDeclaredOutput storage.Class = "declared-output"
	classLikelyOutput   storage.Class = "likely-output"
	classOther          storage.Class = "other-local"
)

// StorageCache is a per-user tool cache and how it relates to the lane volume.
type StorageCache struct {
	storage.CacheLocation
	Exists bool `json:"exists"`
	// SameVolumeAsLanes says whether hard links or clones between the lane and
	// this cache can work. Link-based reuse across volumes falls back to copying.
	SameVolumeAsLanes bool `json:"sameVolumeAsLanes"`
}

// StorageMapping is a registered shared dependency link. Its target is not
// measured: it belongs to every lane that links it.
type StorageMapping struct {
	Path              string `json:"path"`
	Target            string `json:"target"`
	Strategy          string `json:"strategy"`
	SameVolumeAsLanes bool   `json:"sameVolumeAsLanes"`
}

// ArtifactsLayout is a .NET centralised output directory a tracked
// Directory.Build.props already asks for. It is read, never written.
type ArtifactsLayout struct {
	Props     string `json:"props"`
	Directory string `json:"directory"`
	Basis     string `json:"basis"`
}

// StorageReport is the read-only answer to "what does this lane cost".
type StorageReport struct {
	Lane      string            `json:"lane"`
	Path      string            `json:"path"`
	Account   storage.Account   `json:"account"`
	Volume    storage.Volume    `json:"volume"`
	Mappings  []StorageMapping  `json:"sharedMappings,omitempty"`
	Caches    []StorageCache    `json:"toolCaches,omitempty"`
	Artifacts []ArtifactsLayout `json:"dotnetArtifactsLayouts,omitempty"`
}

// likelyOutputDirectories are directory names that conventionally hold
// generated content. They only apply to files the lane does not track.
var likelyOutputDirectories = map[string]bool{
	"bin": true, "obj": true, "node_modules": true, "dist": true, "build": true, "out": true,
	"target": true, "artifacts": true, "testresults": true, "coverage": true, ".next": true, "__pycache__": true,
}

var (
	useArtifactsOutput = regexp.MustCompile(`(?is)<UseArtifactsOutput>\s*true\s*</UseArtifactsOutput>`)
	artifactsPathValue = regexp.MustCompile(`(?is)<ArtifactsPath>\s*([^<]*?)\s*</ArtifactsPath>`)
)

const (
	thisDirectory  = `$(MSBuildThisFileDirectory)`
	propsReadLimit = 64 << 10
)

// artifactsLayouts finds centralised output directories declared by tracked
// Directory.Build.props files. Anything it cannot place inside the lane with
// certainty is left out rather than guessed.
func (e *Engine) artifactsLayouts(opened *lane) []ArtifactsLayout {
	names := []string{}
	for name := range opened.snapshot.Baseline.Files {
		if path.Base(name) == "Directory.Build.props" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	layouts := []ArtifactsLayout{}
	for _, name := range names {
		body, err := opened.root.ReadFileLimit(name, propsReadLimit)
		if err != nil {
			continue
		}
		directory := path.Dir(name)
		if useArtifactsOutput.Match(body) {
			layouts = append(layouts, ArtifactsLayout{Props: name, Directory: path.Join(directory, "artifacts"), Basis: "UseArtifactsOutput is true"})
			continue
		}
		match := artifactsPathValue.FindSubmatch(body)
		if match == nil {
			continue
		}
		value := string(match[1])
		if !strings.HasPrefix(value, thisDirectory) {
			continue
		}
		relative := strings.Trim(strings.ReplaceAll(strings.TrimPrefix(value, thisDirectory), `\`, "/"), "/")
		if relative == "" || strings.Contains(relative, "$(") || strings.Contains(relative, "..") || path.IsAbs(relative) {
			continue
		}
		layouts = append(layouts, ArtifactsLayout{Props: name, Directory: path.Join(directory, relative), Basis: "ArtifactsPath is under the props directory"})
	}
	return layouts
}

// classifier maps a lane-relative path to its storage class.
func (l *lane) classifier(layouts []ArtifactsLayout) storage.Classifier {
	contextFiles := l.contextSet()
	return func(relative string) storage.Class {
		switch {
		case strings.HasPrefix(relative, ".workcopy/"):
			return classMetadata
		}
		if _, tracked := l.snapshot.Baseline.Files[relative]; tracked {
			return classSource
		}
		if _, copied := contextFiles[relative]; copied {
			return classAgentContext
		}
		if l.discardable(relative) {
			return classDeclaredOutput
		}
		for _, layout := range layouts {
			if relative == layout.Directory || strings.HasPrefix(relative, layout.Directory+"/") {
				return classLikelyOutput
			}
		}
		segments := strings.Split(relative, "/")
		for _, segment := range segments[:len(segments)-1] {
			if likelyOutputDirectories[strings.ToLower(segment)] {
				return classLikelyOutput
			}
		}
		return classOther
	}
}

// toolCaches reports the per-user caches and whether each shares a volume with
// the lanes. A cache that cannot be inspected is left out of the list.
func toolCaches(laneVolume storage.Volume) []StorageCache {
	home, _ := os.UserHomeDir()
	userCache, _ := os.UserCacheDir()
	caches := []StorageCache{}
	for _, location := range storage.ToolCaches(os.LookupEnv, home, userCache) {
		cache := StorageCache{CacheLocation: location}
		if _, err := os.Stat(location.Path); err == nil {
			cache.Exists = true
		}
		if volume, err := storage.Describe(location.Path); err == nil {
			cache.SameVolumeAsLanes = storage.SameVolume(laneVolume, volume)
		}
		caches = append(caches, cache)
	}
	return caches
}

// Storage measures one lane without changing it. It takes no lock, so the
// numbers are a point-in-time estimate while a build or an agent is writing.
func (e *Engine) Storage(ctx context.Context, name string) (Report, error) {
	report := Report{Command: "storage"}
	opened, err := e.openLane(name)
	if err != nil {
		report.Workspace = e.missingView(name, err)
		return report, err
	}
	defer opened.close()
	layouts := e.artifactsLayouts(opened)
	account, err := storage.Measure(ctx, opened.root.Path, opened.root.Walk, opened.classifier(layouts))
	if err != nil {
		return report, err
	}
	detail := &StorageReport{Lane: name, Path: opened.root.Path, Account: account, Artifacts: layouts}
	detail.Volume, err = storage.Describe(opened.root.Path)
	if err != nil {
		report.Warnings = append(report.Warnings, "The lane volume could not be inspected: "+err.Error())
	}
	for _, mapping := range opened.snapshot.Manifest.Mappings {
		linked := StorageMapping{Path: mapping.Path, Target: mapping.Target, Strategy: mapping.Strategy}
		if volume, describeErr := storage.Describe(mapping.Target); describeErr == nil {
			linked.SameVolumeAsLanes = storage.SameVolume(detail.Volume, volume)
		}
		detail.Mappings = append(detail.Mappings, linked)
	}
	detail.Caches = toolCaches(detail.Volume)
	report.Storage = detail
	report.Workspace = view(opened.snapshot)
	report.Generation = opened.snapshot.Manifest.Generation
	report.Message = storageMessage(detail)
	report.Warnings = append(report.Warnings, storageWarnings(detail)...)
	return report, nil
}

// volumeLine is one doctor line about the volume under a path. An unreadable
// volume is said so; it is never guessed.
func volumeLine(label, location string) string {
	volume, err := storage.Describe(location)
	if err != nil {
		return fmt.Sprintf("%s: not inspected (%v)", label, err)
	}
	line := fmt.Sprintf("%s: %s, hard links %s, block clone %s, %s free of %s", label, volume.FileSystem, volume.HardLinks, volume.BlockClone, humanBytes(int64(volume.FreeBytes)), humanBytes(int64(volume.TotalBytes)))
	if volume.CloneBasis != "" {
		line += " (" + volume.CloneBasis + ")"
	}
	return line
}

func storageWarnings(detail *StorageReport) []string {
	warnings := []string{}
	if detail.Account.Unreadable > 0 {
		warnings = append(warnings, fmt.Sprintf("%d files could not be read, so these totals are a lower bound.", detail.Account.Unreadable))
	}
	if detail.Volume.HardLinks == storage.No {
		warnings = append(warnings, "This volume does not support hard links, so link-based reuse cannot work here.")
	}
	if detail.Volume.BlockClone == storage.Yes {
		warnings = append(warnings, "This volume shares data blocks between cloned files. Allocation is counted per file, so these figures are an upper bound.")
	}
	for _, cache := range detail.Caches {
		if cache.Exists && !cache.SameVolumeAsLanes {
			warnings = append(warnings, fmt.Sprintf("The %s (%s) is on a different volume than the lanes. Hard-link or clone based reuse between them falls back to copying, usually without any message.", cache.Tool, cache.Path))
		}
	}
	for _, mapping := range detail.Mappings {
		if !mapping.SameVolumeAsLanes {
			warnings = append(warnings, fmt.Sprintf("The shared mapping %s points at %s, a different volume than the lane.", mapping.Path, mapping.Target))
		}
	}
	return warnings
}

func storageMessage(detail *StorageReport) string {
	lines := []string{
		fmt.Sprintf("Lane %s at %s", detail.Lane, detail.Path),
		fmt.Sprintf("Volume: %s, hard links %s, block clone %s, %s free of %s", detail.Volume.FileSystem, detail.Volume.HardLinks, detail.Volume.BlockClone, humanBytes(int64(detail.Volume.FreeBytes)), humanBytes(int64(detail.Volume.TotalBytes))),
		fmt.Sprintf("Lane total: %d files, %s logical, %s allocated (%s reclaimable if deleted, %s shared with files outside the lane)", detail.Account.Total.Files, humanBytes(detail.Account.Total.LogicalBytes), humanBytes(detail.Account.Total.AllocatedBytes), humanBytes(detail.Account.Total.ReclaimableBytes), humanBytes(detail.Account.Total.SharedBytes)),
	}
	classes := make([]string, 0, len(detail.Account.Classes))
	for class := range detail.Account.Classes {
		classes = append(classes, string(class))
	}
	sort.Strings(classes)
	for _, class := range classes {
		t := detail.Account.Classes[storage.Class(class)]
		lines = append(lines, fmt.Sprintf("  %-18s %7d files  %10s allocated  %10s reclaimable  %10s shared", class, t.Files, humanBytes(t.AllocatedBytes), humanBytes(t.ReclaimableBytes), humanBytes(t.SharedBytes)))
	}
	if len(detail.Account.Largest) > 0 {
		lines = append(lines, "Largest directories (top level, by allocation):")
		for index, place := range detail.Account.Largest {
			if index == 8 {
				lines = append(lines, fmt.Sprintf("  ... %d more in the JSON report", len(detail.Account.Largest)-index))
				break
			}
			lines = append(lines, fmt.Sprintf("  %10s  %-18s %s (%d files)", humanBytes(place.AllocatedBytes), place.Class, place.Path, place.Files))
		}
	}
	if links := len(detail.Account.Links) + detail.Account.LinksOmitted; links > 0 {
		lines = append(lines, fmt.Sprintf("Not entered: %d links (shared dependency mappings and other links; their targets belong to other places)", links))
	}
	for _, layout := range detail.Artifacts {
		lines = append(lines, fmt.Sprintf("Centralised .NET output: %s (from %s, %s)", layout.Directory, layout.Props, layout.Basis))
	}
	lines = append(lines, "Read-only: nothing was changed. Figures are a point-in-time estimate, and build tools can write outside the lane.")
	return strings.Join(lines, "\n")
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, suffix := float64(n), []string{"KiB", "MiB", "GiB", "TiB"}
	for index := 0; index < len(suffix); index++ {
		value /= unit
		if value < unit || index == len(suffix)-1 {
			return fmt.Sprintf("%.1f %s", value, suffix[index])
		}
	}
	return fmt.Sprintf("%d B", n)
}
