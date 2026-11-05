package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"workcopy/internal/fsops"
	"workcopy/internal/model"
	"workcopy/internal/workspace"
)

func doctor(ctx context.Context, a arguments) (workspace.Report, error) {
	if len(a.positionals) != 0 {
		return workspace.Report{Command: "doctor"}, usage(fmt.Errorf("doctor takes no positional arguments"))
	}
	return workspace.Doctor(ctx, Version, a.value("repo"), a.value("root"))
}
func inspectLegacy(a arguments) (workspace.Report, error) {
	report := workspace.Report{Command: "legacy"}
	if len(a.positionals) != 0 || a.value("workspace") == "" {
		return report, usage(fmt.Errorf("legacy requires --workspace OLD-LANE and takes no positional arguments"))
	}
	directory, err := filepath.Abs(a.value("workspace"))
	if err != nil {
		return report, err
	}
	root, err := fsops.OpenRoot(directory)
	if err != nil {
		return report, err
	}
	defer root.Close()
	type inspected struct {
		Path      string `json:"path"`
		Bytes     int    `json:"bytes"`
		SHA256    string `json:"sha256"`
		ValidJSON bool   `json:"validJson"`
	}
	found := []inspected{}
	for _, name := range []string{".workcopy/manifest.json", ".workcopy/baseline.json", ".workcopy/config.json", ".workcopy.json"} {
		data, readErr := root.ReadFileLimit(name, 1024*1024)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return report, readErr
		}
		found = append(found, inspected{Path: name, Bytes: len(data), SHA256: model.Hash(data), ValidJSON: json.Valid(data)})
	}
	if len(found) == 0 {
		return report, model.Fail(3, "legacy-metadata-not-found", "No recognized Workcopy metadata files were found; nothing was changed")
	}
	encoded, err := json.MarshalIndent(found, "", "  ")
	if err != nil {
		return report, err
	}
	report.Message = "Read-only legacy inspection; no adoption, conversion, or deletion was performed.\n" + string(encoded)
	report.Warnings = []string{"Legacy resolution may have absorbed local contributions into its baseline. A clean legacy status does not establish that a lane is disposable.", "Create a new Go lane and explicitly review/import old contributions. Automatic migration is not supported."}
	return report, nil
}
