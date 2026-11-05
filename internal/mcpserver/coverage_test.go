package mcpserver

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// calledTools records every tool a test invoked, observed on the client side so
// direct CallTool uses are counted too. TestMain fails the package if the
// server offers a tool that no test called.
var calledTools sync.Map

// observe records tools/call requests sent by a test client.
func observe(client *mcp.Client) {
	client.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				if params, ok := req.GetParams().(*mcp.CallToolParams); ok {
					calledTools.Store(params.Name, true)
				}
			}
			return next(ctx, method, req)
		}
	})
}

func fullRun() bool {
	if f := flag.Lookup("test.run"); f != nil && f.Value.String() != "" {
		return false
	}
	if f := flag.Lookup("test.short"); f != nil && f.Value.String() == "true" {
		return false
	}
	return true
}

// offeredTools lists the tools a server advertises.
func offeredTools() ([]string, error) {
	server := New(nil, "coverage", Options{})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = server.Run(ctx, serverTransport) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "coverage", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names, nil
}

func verifyToolCoverage() error {
	names, err := offeredTools()
	if err != nil {
		return err
	}
	if len(names) < 15 {
		return fmt.Errorf("server offered only %d tools: %v", len(names), names)
	}
	missing := []string{}
	for _, name := range names {
		if _, ok := calledTools.Load(name); !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("MCP tools never called by a test: %s", strings.Join(missing, ", "))
	}
	return nil
}
