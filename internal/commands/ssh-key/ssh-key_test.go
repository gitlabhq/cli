//go:build !integration

package ssh

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmdSSHKey(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	factory := cmdtest.NewTestFactory(ios)

	cmd := NewCmdSSHKey(factory)

	assert.NotNil(t, cmd)
	assert.Equal(t, "ssh-key <command>", cmd.Use)
	assert.Equal(t, "Manage SSH keys registered with your GitLab account.", cmd.Short)
	assert.True(t, cmd.HasSubCommands())

	// Check that all expected subcommands are present
	subcommands := cmd.Commands()
	subcommandNames := make([]string, len(subcommands))
	for i, subcmd := range subcommands {
		subcommandNames[i] = subcmd.Use
	}

	expectedSubcommands := []string{"add [key-file]", "delete [<key-id>]", "get [<key-id>]", "list"}
	for _, expected := range expectedSubcommands {
		assert.Contains(t, subcommandNames, expected)
	}
}
