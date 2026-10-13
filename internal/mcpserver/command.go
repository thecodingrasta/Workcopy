package mcpserver

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workcopy/internal/workspace"
)

// Command runs a local stdio server. stdout is exclusively MCP protocol data;
// startup errors/help go to stderr.
func Command(ctx context.Context, args []string, version string, stdin io.ReadCloser, stdout io.WriteCloser, stderr io.Writer) int {
	flags := flag.NewFlagSet("workcopy mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", "", "Optional default source checkout; tools can select other repositories")
	root := flags.String("root", "", "Default storage parent; with --repo, its exact lane root. Tools can override")
	config := flags.String("config", "", "Owner-supplied selection configuration; tools cannot override this path")
	maxLanes := flags.Int("max-lanes", 32, "Lane admission limit per repository/root; 0 disables the count limit")
	reserve := flags.Uint64("reserve-mib", 2048, "Minimum free disk reserve in MiB (at least 2048)")
	var targets []string
	flags.Func("allow-share-target", "Owner-approved existing dependency directory (repeatable)", func(value string) error {
		absolute, err := filepath.Abs(value)
		if err != nil {
			return err
		}
		targets = append(targets, filepath.Clean(absolute))
		return nil
	})
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *maxLanes < 0 || *reserve < 2048 || *reserve > 1<<40 || (*config != "" && *repo == "") {
		fmt.Fprintln(stderr, "mcp requires no positional arguments, nonnegative --max-lanes and --reserve-mib >= 2048; --config requires --repo")
		return 2
	}
	var engine *workspace.Engine
	var err error
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
	}
	server := New(engine, version, Options{Root: *root, MaxLanes: *maxLanes, ReserveBytes: *reserve << 20, ShareTargets: targets, ConfigPath: *config})
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
