//go:build !integration

package path

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestConfigPath_PrintsConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GLAB_CONFIG_DIR", dir)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	out, err := exec("")
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, "config.yml"), strings.TrimSpace(out.String()))
	assert.Empty(t, out.Stderr())
}

// The path has to be reported before the first login, so a missing file is not
// an error.
func TestConfigPath_PrintsConfigFileWhenItDoesNotExist(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("GLAB_CONFIG_DIR", dir)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	out, err := exec("")
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, "config.yml"), strings.TrimSpace(out.String()))
}

func TestConfigPath_DirPrintsDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GLAB_CONFIG_DIR", dir)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	out, err := exec("--dir")
	require.NoError(t, err)

	assert.Equal(t, dir, strings.TrimSpace(out.String()))
	assert.Empty(t, out.Stderr())
}

func TestConfigPath_RejectsArgs(t *testing.T) {
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("extra-arg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command")
}
