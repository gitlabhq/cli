//go:build !integration

package cmdtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestIsolateConfig(t *testing.T) {
	t.Cleanup(xdg.Reload)
	for _, name := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_RUNTIME_DIR", "GLAB_CONFIG_DIR", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		t.Setenv(name, "/developer/"+name)
	}

	cleanup := IsolateConfig()
	dir := filepath.Dir(os.Getenv("GLAB_CONFIG_DIR"))
	assert.True(t, strings.HasPrefix(filepath.Base(dir), "glab-test-config-"), dir)
	for _, name := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_RUNTIME_DIR", "GLAB_CONFIG_DIR"} {
		assert.DirExists(t, os.Getenv(name))
		assert.Equal(t, dir, filepath.Dir(os.Getenv(name)), name)
	}
	assert.Equal(t, os.Getenv("HOME"), os.Getenv("USERPROFILE"))
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		_, set := os.LookupEnv(name)
		assert.False(t, set, "%s must be unset", name)
	}
	assert.Equal(t, os.Getenv("GLAB_CONFIG_DIR"), config.ConfigDir())

	// A test that clears GLAB_CONFIG_DIR and sets HOME gets the XDG
	// default, which must be in the temporary directory too.
	t.Setenv("GLAB_CONFIG_DIR", "")
	t.Setenv("HOME", t.TempDir())
	assert.Equal(t, filepath.Join(dir, "xdg-config", "glab-cli"), config.ConfigDir())

	cleanup()
	_, err := os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist)
}
