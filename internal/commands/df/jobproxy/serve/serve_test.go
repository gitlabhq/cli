//go:build !integration

package serve

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestServeRequiresStateDir(t *testing.T) {
	t.Parallel()
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("")
	assert.ErrorContains(t, err, `required flag(s) "state-dir" not set`)
}

// Not parallel: it sets CI_PROJECT_DIR.
func TestServeAnchorsTheCILogToTheProjectDir(t *testing.T) {
	t.Setenv("CI_PROJECT_DIR", "/builds/group/project")
	o := &options{}
	require.NoError(t, o.complete())
	assert.Equal(t, "/builds/group/project", o.baseDir)
}

// Not parallel: it clears CI_PROJECT_DIR and changes the working directory.
func TestServeAnchorsTheCILogToTheWorkingDirOutsideCI(t *testing.T) {
	t.Setenv("CI_PROJECT_DIR", "")
	dir := t.TempDir()
	t.Chdir(dir)
	o := &options{}
	require.NoError(t, o.complete())
	assert.Equal(t, dir, o.baseDir)
}
