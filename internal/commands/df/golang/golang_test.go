//go:build !integration

package golang

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmdUse(t *testing.T) {
	t.Parallel()
	cmd := NewCmd(cmdtest.NewTestFactory(nil))
	assert.Equal(t, "go <go args>", cmd.Use)
}

func TestNewCmdEnvironmentHelp(t *testing.T) {
	t.Parallel()
	cmd := NewCmd(cmdtest.NewTestFactory(nil))
	env := cmd.Annotations["help:environment"]
	assert.Contains(t, env, "HTTPS_PROXY:")
	assert.NotContains(t, env, "HTTP_PROXY:", "go downloads modules with its own proxy settings, so glab's plain-HTTP fallback doesn't apply")
}
