//go:build !integration

package start

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/session"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// Not parallel: it sets the state-directory variable.
func TestStartPrintsFalseWhenItCannotStart(t *testing.T) {
	// A file where the state directory should be makes start fail before it
	// spawns the daemon.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	t.Setenv(session.StateDirEnv, filepath.Join(blocker, "state"))

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	out, err := exec("")

	require.Error(t, err)
	// The output is eval'd, so it must make the eval fail rather than be empty.
	assert.Equal(t, "false\n", out.OutBuf.String())
}

func TestStartRejectsArguments(t *testing.T) {
	t.Parallel()
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("extra")
	assert.Error(t, err)
}
