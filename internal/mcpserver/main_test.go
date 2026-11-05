package mcpserver

import (
	"fmt"
	"os"
	"testing"

	"workcopy/internal/testutil"
)

func TestMain(m *testing.M) {
	cleanup := testutil.IsolateSettings()
	code := m.Run()
	if code == 0 && fullRun() {
		if err := verifyToolCoverage(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}
	cleanup()
	os.Exit(code)
}
