package cli

import (
	"os"
	"testing"

	"workcopy/internal/testutil"
)

func TestMain(m *testing.M) {
	cleanup := testutil.IsolateSettings()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
