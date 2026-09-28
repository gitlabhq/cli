//go:build !integration

package stackswitch

import (
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestSwitchWithStackName(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("current-stack")
	require.NoError(t, err)
	_, err = git.AddStackRefDir("target-stack")
	require.NoError(t, err)
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "current-stack"))

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdStackSwitch(f, nil)
	}, true)

	output, err := exec("target-stack")

	require.NoError(t, err)
	require.Equal(t, "Switched to stack target-stack.\n", output.String())

	currentStack, err := git.GetCurrentStackTitle()
	require.NoError(t, err)
	require.Equal(t, "target-stack", currentStack)
}

func TestSwitchWithCurrentStackName(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("current-stack")
	require.NoError(t, err)
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "current-stack"))

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdStackSwitch(f, nil)
	}, true)

	output, err := exec("current-stack")

	require.NoError(t, err)
	require.Empty(t, output.String())

	currentStack, err := git.GetCurrentStackTitle()
	require.NoError(t, err)
	require.Equal(t, "current-stack", currentStack)
}

func TestSwitchPromptsForStackName(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("current-stack")
	require.NoError(t, err)
	_, err = git.AddStackRefDir("target-stack")
	require.NoError(t, err)
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "current-stack"))

	c := ugh.New(t)
	c.Expect(ugh.Select("Choose a stack to switch to:")).
		Do(ugh.SelectIndex(1))

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdStackSwitch(f, nil)
	}, true, cmdtest.WithConsole(t, c))

	output, err := exec("")

	require.NoError(t, err)
	require.Contains(t, output.String(), "Switched to stack target-stack.\n")

	currentStack, err := git.GetCurrentStackTitle()
	require.NoError(t, err)
	require.Equal(t, "target-stack", currentStack)
}

func TestSwitchWithoutStackNameRequiresPrompts(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("current-stack")
	require.NoError(t, err)
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "current-stack"))

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdStackSwitch(f, nil)
	}, false)

	_, err = exec("")

	var flagErr cmdutils.FlagError
	require.ErrorAs(t, err, &flagErr)
	require.EqualError(t, err, "switching stacks failed: the <stack-name> argument is required when prompts are disabled")
}

func TestSwitchWithoutStackNameRequiresExistingStacks(t *testing.T) {
	git.InitGitRepo(t)

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdStackSwitch(f, nil)
	}, true)

	_, err := exec("")

	require.EqualError(t, err, "switching stacks failed: no stacks found; create one with \"glab stack create\"")
}

func TestSwitchBlockedByInProgressReorder(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("current-stack")
	require.NoError(t, err)
	_, err = git.AddStackRefDir("target-stack")
	require.NoError(t, err)
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "current-stack"))
	require.NoError(t, git.WriteReorderState("current-stack",
		git.ReorderState{NewOrder: []string{"branchA"}}))

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdStackSwitch(f, nil)
	}, true)

	_, err = exec("target-stack")

	require.ErrorContains(t, err, "stack reorder is in progress")

	currentStack, err := git.GetCurrentStackTitle()
	require.NoError(t, err)
	require.Equal(t, "current-stack", currentStack)
}
