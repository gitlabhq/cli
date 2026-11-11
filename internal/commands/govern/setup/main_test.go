//go:build !integration

package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// TestMain also unsets XDG_CONFIG_HOME, which keeps the OpenCode plugin
// under the HOME each test sets.
func TestMain(m *testing.M) {
	cleanup := cmdtest.IsolateConfig()
	_ = os.Unsetenv("XDG_CONFIG_HOME")
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// Uninstall turns off discovery in glab's config directory, so no test may
// reach the developer's.
func TestMain_IsolatesGlabState(t *testing.T) {
	dir := config.ConfigDir()
	assert.True(t, strings.HasPrefix(filepath.Base(filepath.Dir(dir)), "glab-test-config-"), dir)
}
