package mcpserver

import (
	"fmt"
	"io"
)

const maxRequestBytes = 1 << 20

// boundedInput limits each newline-framed request before the SDK decodes it.
// The SDK still owns JSON-RPC and MCP protocol semantics.
type boundedInput struct {
	io.ReadCloser
	frameBytes int
}

func (r *boundedInput) Read(body []byte) (int, error) {
	n, err := r.ReadCloser.Read(body)
	for i, b := range body[:n] {
		if b == '\n' {
			r.frameBytes = 0
		} else {
			r.frameBytes++
			if r.frameBytes > maxRequestBytes {
				return i, fmt.Errorf("MCP request exceeds 1 MiB frame limit")
			}
		}
	}
	return n, err
}
