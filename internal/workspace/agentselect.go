package workspace

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"workcopy/internal/agents"
	"workcopy/internal/model"
)

type clientKey struct{}

// WithClient records the name an MCP client announced in its initialize
// request, so a lane created during the call can serve that client's tool.
func WithClient(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, clientKey{}, strings.TrimSpace(name))
}

func clientName(ctx context.Context) string {
	name, _ := ctx.Value(clientKey{}).(string)
	return name
}

// validateAgents rejects a tool name this version does not know, so a typo is
// an error rather than a lane that silently receives no tool files.
func validateAgents(words []string) error {
	for _, word := range words {
		if !agents.ValidSelector(word) {
			return model.Fail(2, "unknown-agent", fmt.Sprintf("%q is not auto, all, generic or one of: %s", word, strings.Join(agents.Keys(), ", ")))
		}
	}
	return nil
}

// recordSelection is the form stored in a lane's manifest.
func recordSelection(selection agents.Resolution) []string {
	switch {
	case selection.All:
		return []string{agents.SelectAll}
	case len(selection.Keys) == 0:
		return []string{agents.GenericKey}
	}
	return slices.Clone(selection.Keys)
}

// recordedSelection turns a manifest record back into a selection.
func recordedSelection(record []string) agents.Resolution {
	keys := []string{}
	for _, word := range record {
		switch word {
		case agents.SelectAll:
			return agents.Resolution{All: true, Source: "recorded when the lane was created"}
		case agents.GenericKey:
		default:
			keys = append(keys, word)
		}
	}
	return agents.Resolution{Keys: keys, Source: "recorded when the lane was created"}
}

// selectAgents settles which agent tools a lane serves.
//
// An explicit request (an MCP argument or a command-line flag) always wins and,
// for an existing lane, replaces what it recorded. Otherwise a lane keeps the
// selection it recorded when it was created; a lane that predates the selection
// keeps every tool; and a repository preview or a lane about to be created takes
// the owner's setting, where "auto" lets the announced MCP client decide and an
// unknown client selects every tool.
func (e *Engine) selectAgents(ctx context.Context, opened *lane, requested []string) agents.Resolution {
	policy := e.contextPolicy()
	if len(requested) == 0 && policy.AgentsFlagged {
		requested = policy.Agents
	}
	if len(requested) > 0 {
		return agents.Resolve(requested, nil, clientName(ctx))
	}
	if opened != nil {
		if record := opened.snapshot.Manifest.ContextAgents; len(record) > 0 {
			return recordedSelection(record)
		}
		return agents.Resolution{All: true, Source: "this lane predates the agent selection, so it keeps every known tool"}
	}
	return agents.Resolve(nil, policy.Agents, clientName(ctx))
}
