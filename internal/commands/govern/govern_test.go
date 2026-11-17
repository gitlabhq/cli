//go:build !integration

package govern

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestAddSubcommands_AttachesGovernanceCommands(t *testing.T) {
	f := cmdtest.NewTestFactory(nil)
	cmd := &cobra.Command{Use: "minder <command>"}
	AddSubcommands(cmd, f)

	for _, args := range [][]string{{"audit", "sync"}, {"doctor"}, {"setup"}} {
		found, _, err := cmd.Find(args)
		if assert.NoError(t, err) {
			assert.Equal(t, args[len(args)-1], found.Name())
		}
	}
}

func TestNewLegacyCmd_RunsTheGovernanceCommands(t *testing.T) {
	f := cmdtest.NewTestFactory(nil)
	legacy := NewLegacyCmd(f)

	assert.Equal(t, "govern", legacy.Name())
	assert.True(t, legacy.Hidden, "glab govern is not listed, but the hooks earlier glab versions installed still run it")

	for _, args := range [][]string{{"audit", "sync"}, {"doctor"}, {"setup"}} {
		cmd, _, err := legacy.Find(args)
		if assert.NoError(t, err) {
			assert.Equal(t, args[len(args)-1], cmd.Name())
		}
	}
}

func TestNewLegacyCmd_IsExcludedFromMCP(t *testing.T) {
	f := cmdtest.NewTestFactory(nil)

	var walk func(cmd *cobra.Command, want bool)
	walk = func(cmd *cobra.Command, want bool) {
		assert.Equal(t, want, cmd.Annotations[mcpannotations.Exclude] == "true", cmd.CommandPath())
		for _, sub := range cmd.Commands() {
			walk(sub, want)
		}
	}
	walk(NewLegacyCmd(f), true)
}
