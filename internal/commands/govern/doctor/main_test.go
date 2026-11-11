//go:build !integration

package doctor

import (
	"os"
	"testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// TestMain also unsets XDG_CONFIG_HOME, which keeps the OpenCode plugin
// check under the HOME each test sets.
func TestMain(m *testing.M) {
	cleanup := cmdtest.IsolateConfig()
	_ = os.Unsetenv("XDG_CONFIG_HOME")
	code := m.Run()
	cleanup()
	os.Exit(code)
}
