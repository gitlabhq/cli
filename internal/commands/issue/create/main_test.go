//go:build !integration

package create

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// The create commands save a recover file in glab's config directory when
// they fail, so no test may reach the developer's.
func TestMain(m *testing.M) {
	cleanup := cmdtest.IsolateConfig()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func TestMain_IsolatesGlabState(t *testing.T) {
	dir := config.ConfigDir()
	assert.True(t, strings.HasPrefix(filepath.Base(filepath.Dir(dir)), "glab-test-config-"), dir)
}
