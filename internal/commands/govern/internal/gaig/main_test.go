//go:build !integration

package gaig

import (
	"os"
	"testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMain(m *testing.M) {
	cleanup := cmdtest.IsolateConfig()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
