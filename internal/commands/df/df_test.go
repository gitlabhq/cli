//go:build !integration

package df

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmdHasSubcommands(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	factory := cmdtest.NewTestFactory(ios)

	cmd := NewCmd(factory)

	assert.Equal(t, "dependency-firewall", cmd.Name())
	assert.Contains(t, cmd.Aliases, "df")

	assert.True(t, cmd.HasSubCommands())

	subcommandNames := make([]string, 0, len(cmd.Commands()))
	for _, subcmd := range cmd.Commands() {
		subcommandNames = append(subcommandNames, subcmd.Name())
	}

	assert.Contains(t, subcommandNames, "npm")
	assert.Contains(t, subcommandNames, "ci-summary")
}

// None of the df subcommands should advertise --repo. ci-summary resolves no
// GitLab project, and the package-manager wrappers forward every argument
// verbatim to the underlying binary (DisableFlagParsing), resolving the
// project from the git remote instead. Advertising a flag that cannot affect
// the outcome is worse than not having it.
func TestNoRepoOverrideAdvertised(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	cmd := NewCmd(cmdtest.NewTestFactory(ios))

	assert.Nil(t, cmd.PersistentFlags().Lookup("repo"),
		"the df parent must not enable --repo for its subcommands")

	for _, subcmd := range cmd.Commands() {
		// LocalFlags is what help and gen-docs render.
		assert.Nil(t, subcmd.LocalFlags().Lookup("repo"),
			"%s does not use a GitLab project, so it must not advertise --repo", subcmd.Name())
	}
}
