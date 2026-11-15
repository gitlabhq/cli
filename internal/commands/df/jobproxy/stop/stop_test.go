//go:build !integration

package stop

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/session"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// Not parallel: it sets the state-directory variable.
func TestStopWithoutARunningProxy(t *testing.T) {
	t.Setenv(session.StateDirEnv, t.TempDir())
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("")
	assert.ErrorContains(t, err, "no running Dependency Firewall proxy")
}

// Not parallel: it clears the state-directory variable.
func TestStopNeedsTheStateDirFromStart(t *testing.T) {
	t.Setenv(session.StateDirEnv, "")
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("")
	assert.ErrorContains(t, err, session.StateDirEnv+" is not set")
}
