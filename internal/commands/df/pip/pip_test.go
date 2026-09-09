//go:build !integration

package pip

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmdUse(t *testing.T) {
	t.Parallel()
	cmd := NewCmd(cmdtest.NewTestFactory(nil))
	assert.Equal(t, "pip <pip args>", cmd.Use)
}
