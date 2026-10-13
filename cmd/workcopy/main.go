// Workcopy is a native command-line utility. It deliberately delegates Git
// semantics to the installed Git executable instead of maintaining a Git clone.
package main

import (
	"context"
	"os"
	"os/signal"

	"workcopy/internal/cli"
	"workcopy/internal/mcpserver"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		os.Exit(mcpserver.Command(ctx, os.Args[2:], cli.Version, os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
