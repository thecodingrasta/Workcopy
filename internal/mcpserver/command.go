package mcpserver

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workcopy/internal/settings"
	"workcopy/internal/workspace"
)

// Command runs a local stdio server. stdout is exclusively MCP protocol data;
// startup errors/help go to stderr.
//
// Every option can also be set once in the user's settings file or an
// environment variable (see `workcopy settings`); a flag given here wins.
func Command(ctx context.Context, args []string, version string, stdin io.ReadCloser, stdout io.WriteCloser, stderr io.Writer) int {
	flags := flag.NewFlagSet("workcopy mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", "", "Optional default source checkout; tools can select other repositories")
	root := flags.String("root", "", "Default storage parent; with --repo, its exact lane root. Tools can override")
	config := flags.String("config", "", "Owner-supplied selection configuration; tools cannot override this path")
	maxLanes := flags.Int("max-lanes", 32, "Lane admission limit per repository/root; 0 disables the count limit")
	reserve := flags.Uint64("reserve-mib", 2048, "Minimum free disk reserve in MiB (at least 2048)")
	timeout := flags.Duration("tool-timeout", DefaultToolTimeout, "Per-call deadline for a tool (10s to 24h); raise it for very large repositories")
	settingsFile := flags.String("settings", "", "Settings file; defaults to WORKCOPY_SETTINGS or the per-user workcopy.env")
	var rawTargets []string
	flags.Func("allow-share-target", "Owner-approved existing dependency directory (repeatable)", func(value string) error {
		rawTargets = append(rawTargets, value)
		return nil
	})
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	// Only flags actually given override the settings file and environment.
	// Without --repo, --root is the parent under which each repository gets its
	// own directory, so it must not hide a repository's own settings file. It is
	// applied after resolution instead, as the parent default.
	repoGiven := flags.Lookup("repo").Value.String() != ""
	explicitRoot := *root
	given := map[string][]string{}
	flags.Visit(func(f *flag.Flag) {
		for _, d := range settings.Definitions {
			if d.Flag == f.Name {
				if f.Name == "root" && !repoGiven {
					continue
				}
				if f.Name == "allow-share-target" {
					given[d.Key] = rawTargets
				} else {
					given[d.Key] = []string{f.Value.String()}
				}
			}
		}
	})
	resolved, err := settings.Load(settings.DefaultInputs(*settingsFile, given))
	if err != nil {
		fmt.Fprintln(stderr, "MCP settings:", err)
		return 2
	}
	if err = resolved.ExportProcessEnvironment(os.LookupEnv, os.Setenv); err != nil {
		fmt.Fprintln(stderr, "MCP settings:", err)
		return 2
	}
	*repo, *root, *config = resolved.String(settings.Repo), resolved.String(settings.Root), resolved.String(settings.Config)
	if !repoGiven && explicitRoot != "" {
		*root = explicitRoot
	}
	*maxLanes, *reserve, *timeout = resolved.Int(settings.MaxLanes), uint64(resolved.Int(settings.ReserveMiB)), resolved.Duration(settings.ToolTimeout)
	var targets []string
	for _, value := range resolved.List(settings.AllowShareTarget) {
		absolute, absErr := filepath.Abs(value)
		if absErr != nil {
			fmt.Fprintln(stderr, absErr)
			return 2
		}
		targets = append(targets, filepath.Clean(absolute))
	}
	if flags.NArg() != 0 || *maxLanes < 0 || *reserve < 2048 || *reserve > 1<<40 || (*config != "" && *repo == "") {
		fmt.Fprintln(stderr, "mcp requires no positional arguments, nonnegative --max-lanes and --reserve-mib >= 2048; --config requires --repo")
		return 2
	}
	if *timeout < 10*time.Second || *timeout > 24*time.Hour {
		fmt.Fprintln(stderr, "mcp --tool-timeout must be between 10s and 24h")
		return 2
	}
	var engine *workspace.Engine
	if *root != "" {
		*root, err = filepath.Abs(*root)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	if *repo != "" {
		engine, err = workspace.New(ctx, *repo, *root)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 3
		}
		if layered, layerErr := projectSettings(resolved, engine.Repository.CommonDir); layerErr != nil {
			fmt.Fprintln(stderr, "MCP settings:", layerErr)
			return 2
		} else if layered != resolved {
			// The repository's own lane root and owner configuration apply unless
			// a flag or environment variable already chose.
			resolved = layered
			if err = resolved.ExportProcessEnvironment(os.LookupEnv, os.Setenv); err != nil {
				fmt.Fprintln(stderr, "MCP settings:", err)
				return 2
			}
			if resolved.Entry(settings.Root).Source == "project" {
				*root = resolved.String(settings.Root)
				if engine, err = workspace.New(ctx, *repo, *root); err != nil {
					fmt.Fprintln(stderr, err)
					return 3
				}
			}
			if resolved.Entry(settings.Config).Source == "project" {
				*config = resolved.String(settings.Config)
			}
		}
		engine.Settings = resolved
	}
	server := New(engine, version, Options{Root: *root, MaxLanes: *maxLanes, ReserveBytes: *reserve << 20, ShareTargets: targets, ConfigPath: *config, ToolTimeout: *timeout, Settings: resolved})
	if server.configErr != nil {
		fmt.Fprintln(stderr, "MCP owner configuration:", server.configErr)
		return 3
	}
	if err = server.Run(ctx, &mcp.IOTransport{Reader: &boundedInput{ReadCloser: stdin}, Writer: stdout}); err != nil {
		fmt.Fprintln(stderr, "MCP server:", err)
		return 5
	}
	return 0
}
