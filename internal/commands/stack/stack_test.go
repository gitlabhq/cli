//go:build !integration

package stack

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func TestStackCmd(t *testing.T) {
	var buf bytes.Buffer
	cmd := NewCmdStack(cmdtest.NewTestFactory(nil))
	cmd.SetOut(&buf)

	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "A stack is a series of small, dependent merge requests that together deliver a feature.")
	assert.Contains(t, buf.String(), text.ExperimentalString)
}
