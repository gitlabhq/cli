//go:build !integration

package poetry

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmdUse(t *testing.T) {
	t.Parallel()
	cmd := NewCmd(cmdtest.NewTestFactory(nil))
	assert.Equal(t, "poetry <poetry args>", cmd.Use)
}
