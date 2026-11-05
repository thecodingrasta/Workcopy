package workspace

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"workcopy/internal/eol"
)

// checkoutPlan answers, for committed paths, how Git itself would write them
// into a working tree: the line-ending rule that applies to each path under the
// pinned commit's attributes and the repository's effective core.* settings.
type checkoutPlan struct {
	config eol.Config
	attrs  map[string]eol.Attributes
}

// rule returns the unresolved line-ending rule for a path.
func (p *checkoutPlan) rule(name string) string {
	return eol.Resolve(p.attrs[name], p.config)
}

// blobToken returns the persisted token for a committed blob.
func (p *checkoutPlan) blobToken(name string, blob []byte) string {
	return eol.ForBlob(p.rule(name), blob)
}

// checkoutPlan reads attributes at the pinned commit (never the dirty working
// tree) and refuses paths that need filters Workcopy does not execute.
func (e *Engine) checkoutPlan(ctx context.Context, commit string, names []string) (*checkoutPlan, error) {
	return e.attributePlan(ctx, commit, names, true)
}

// attributePlan is checkoutPlan with the unsupported-filter refusal optional.
// Rules for paths that exist only in a lane are read leniently: a new local
// file must stay visible to status even if its name matches a filter pattern.
func (e *Engine) attributePlan(ctx context.Context, commit string, names []string, strict bool) (*checkoutPlan, error) {
	plan := &checkoutPlan{attrs: map[string]eol.Attributes{}}
	config, err := e.Repository.CheckoutConfig(ctx)
	if err != nil {
		return nil, err
	}
	plan.config = eol.Config{AutoCRLF: config.AutoCRLF, EOL: config.EOL, NativeCRLF: e.Repository.NativeCRLF()}
	if len(names) == 0 {
		return plan, nil
	}
	input := []byte(strings.Join(names, "\x00") + "\x00")
	body, err := e.Repository.Run(ctx, e.Repository.Root, input, "check-attr", "--source="+commit, "-z", "--all", "--stdin")
	if err != nil {
		return nil, fmt.Errorf("Git 2.43 or newer with check-attr --source is required: %w", err)
	}
	fields := bytes.Split(body, []byte{0})
	for n := 0; n+2 < len(fields); n += 3 {
		path, attribute, value := string(fields[n]), string(fields[n+1]), string(fields[n+2])
		if strict && (attribute == "filter" || attribute == "working-tree-encoding" || attribute == "ident") && value != "unset" && value != "unspecified" {
			return nil, fmt.Errorf("selected path %s requires unsupported attribute %s=%s; external filters and encodings are not executed", path, attribute, value)
		}
		current := plan.attrs[path]
		switch attribute {
		case "text":
			current.Text = value
		case "crlf":
			current.CRLF = value
		case "eol":
			current.EOL = value
		default:
			continue
		}
		plan.attrs[path] = current
	}
	return plan, nil
}
