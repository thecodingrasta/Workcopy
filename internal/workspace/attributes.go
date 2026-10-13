package workspace

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

func (e *Engine) validateAttributes(ctx context.Context, commit string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	input := []byte(strings.Join(names, "\x00") + "\x00")
	body, err := e.Repository.Run(ctx, e.Repository.Root, input, "check-attr", "--source="+commit, "-z", "--all", "--stdin")
	if err != nil {
		return fmt.Errorf("Git 2.43 or newer with check-attr --source is required: %w", err)
	}
	fields := bytes.Split(body, []byte{0})
	for n := 0; n+2 < len(fields); n += 3 {
		attribute, value := string(fields[n+1]), string(fields[n+2])
		if (attribute == "filter" || attribute == "working-tree-encoding" || attribute == "ident") && value != "unset" && value != "unspecified" {
			return fmt.Errorf("selected path %s requires unsupported attribute %s=%s; external filters and encodings are not executed", fields[n], attribute, value)
		}
	}
	return nil
}
