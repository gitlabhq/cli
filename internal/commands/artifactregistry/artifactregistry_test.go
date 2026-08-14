//go:build !integration

package artifactregistry

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmd(t *testing.T) {
	ios, _, stdout, _ := cmdtest.TestIOStreams()
	cmd := NewCmd(cmdtest.NewTestFactory(ios))
	cmd.SetOut(stdout)

	require.NoError(t, cmd.Execute())

	assert.Contains(t, stdout.String(), "status")
	assert.Contains(t, stdout.String(), "get-token")
	assert.Contains(t, stdout.String(), "login")
}

func TestNewCmd_NameAndAlias(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	cmd := NewCmd(cmdtest.NewTestFactory(ios))

	assert.Equal(t, "artifact-registry", cmd.Name())
	assert.Contains(t, cmd.Aliases, "ar")

	// Assert on resolution, not just the Aliases field: `glab ar status` is the
	// promise that has to keep working.
	root := &cobra.Command{Use: "glab"}
	root.AddCommand(cmd)

	found, _, err := root.Find([]string{"ar", "status"})
	require.NoError(t, err)
	assert.Equal(t, "status", found.Name())
}
