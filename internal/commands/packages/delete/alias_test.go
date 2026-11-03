//go:build !integration

package delete_test

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/packages"
	packagesDelete "gitlab.com/gitlab-org/cli/internal/commands/packages/delete"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_PackagesDeleteDelAlias(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockPackages.EXPECT().
		DeleteProjectPackage("OWNER/REPO", int64(1), gomock.Any()).
		Return(nil, nil)

	exec := cmdtest.SetupCmdForTest(t, packages.NewCmd, false, cmdtest.WithGitLabClient(testClient.Client))

	out, err := exec("del 1 -y")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Package 1 deleted.")
}

func Test_PackagesDeleteRmAliasDeprecated(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockPackages.EXPECT().
		DeleteProjectPackage("OWNER/REPO", int64(1), gomock.Any()).
		Return(nil, nil)

	// Cobra prints the deprecation notice through its own output writer,
	// which the production root command never wires to the IOStreams, so the
	// notice lands on stderr. Wire it to a buffer here to assert on it.
	var cobraOut bytes.Buffer
	exec := cmdtest.SetupCmdForTest(
		t,
		func(f cmdutils.Factory) *cobra.Command {
			cmd := packages.NewCmd(f)
			cmd.SetOut(&cobraOut)
			return cmd
		},
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("rm 1 -y")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Package 1 deleted.")
	assert.Contains(t, cobraOut.String(), "Command \"rm\" is deprecated, use `glab packages del` instead.")
}

func Test_PackagesDeleteRmAliasNotAdvertised(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)

	packagesCmd := packages.NewCmd(f)
	for _, c := range packagesCmd.Commands() {
		if c.Name() == "rm" {
			assert.False(t, c.IsAvailableCommand(), "the deprecated rm command must stay hidden from help, completion, and docs")
		}
	}

	deleteCmd := packagesDelete.NewCmd(f)
	assert.Equal(t, []string{"del"}, deleteCmd.Aliases)
}

func Test_PackagesDeleteRmAliasNotAnMCPTool(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)

	// registerToolsFromCommands registers every annotated command as an MCP
	// tool and never looks at Hidden/Deprecated, so the rm twin must opt out
	// via mcp:exclude instead of surfacing as a duplicate of glab_packages_delete.
	rmCmd := packagesDelete.NewRmCmd(f)
	assert.Equal(t, "true", rmCmd.Annotations[mcpannotations.Exclude])
}
