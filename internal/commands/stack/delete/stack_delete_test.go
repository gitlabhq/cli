//go:build !integration

package delete

import (
	"path/filepath"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// stackDir returns the metadata directory a stack of this name would occupy,
// whether or not it currently exists.
func stackDir(t *testing.T, name string) string {
	t.Helper()

	stackLoc, err := git.StackLocation()
	require.NoError(t, err)

	return filepath.Join(stackLoc, name)
}

func TestDeleteWithStackName(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("doomed-stack")
	require.NoError(t, err)
	_, err = git.AddStackRefDir("keeper-stack")
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmdDeleteStack, true)

	output, err := exec("doomed-stack --yes")

	require.NoError(t, err)
	require.Equal(t, "Deleted stack doomed-stack.\n", output.String())
	require.NoDirExists(t, stackDir(t, "doomed-stack"))
	require.DirExists(t, stackDir(t, "keeper-stack"))
}

func TestDeleteWithoutPromptsSucceedsWithNameAndConfirmationFlag(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("doomed-stack")
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmdDeleteStack, false)

	output, err := exec("doomed-stack --yes")

	require.NoError(t, err)
	require.Equal(t, "Deleted stack doomed-stack.\n", output.String())
	require.NoDirExists(t, stackDir(t, "doomed-stack"))
}

func TestDeleteClearsTheCurrentStack(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("current-stack")
	require.NoError(t, err)
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "current-stack"))

	exec := cmdtest.SetupCmdForTest(t, NewCmdDeleteStack, true)

	_, err = exec("current-stack --yes")
	require.NoError(t, err)

	_, err = git.GetCurrentStackTitle()
	require.Error(t, err, "glab.currentstack should have been unset")
}

func TestDeleteKeepsTheCurrentStackWhenDeletingAnother(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("current-stack")
	require.NoError(t, err)
	_, err = git.AddStackRefDir("other-stack")
	require.NoError(t, err)
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "current-stack"))

	exec := cmdtest.SetupCmdForTest(t, NewCmdDeleteStack, true)

	_, err = exec("other-stack --yes")
	require.NoError(t, err)

	currentStack, err := git.GetCurrentStackTitle()
	require.NoError(t, err)
	require.Equal(t, "current-stack", currentStack)
}

func TestDeletePromptsForStackNameAndConfirmation(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("first-stack")
	require.NoError(t, err)
	_, err = git.AddStackRefDir("second-stack")
	require.NoError(t, err)

	c := ugh.New(t)
	c.Expect(ugh.Select("Choose a stack to delete:")).
		Do(ugh.SelectIndex(1))
	c.Expect(ugh.Confirm("Delete stack second-stack?")).
		Do(ugh.Affirm)

	exec := cmdtest.SetupCmdForTest(t, NewCmdDeleteStack, true, cmdtest.WithConsole(t, c))

	output, err := exec("")

	require.NoError(t, err)
	require.Contains(t, output.String(), "Deleted stack second-stack.\n")
	require.NoDirExists(t, stackDir(t, "second-stack"))
	require.DirExists(t, stackDir(t, "first-stack"))
}

func TestDeleteDecliningTheConfirmationKeepsTheStack(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("spared-stack")
	require.NoError(t, err)

	c := ugh.New(t)
	c.Expect(ugh.Confirm("Delete stack spared-stack?")).
		Do(ugh.Reject)

	exec := cmdtest.SetupCmdForTest(t, NewCmdDeleteStack, true, cmdtest.WithConsole(t, c))

	_, err = exec("spared-stack")

	require.ErrorIs(t, err, iostreams.ErrUserCancelled)
	require.EqualError(t, err, "user cancelled")
	require.DirExists(t, stackDir(t, "spared-stack"))
}

func TestDeleteWithoutConfirmationFlagRequiresPrompts(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("a-stack")
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmdDeleteStack, false)

	_, err = exec("a-stack")

	var flagErr cmdutils.FlagError
	require.ErrorAs(t, err, &flagErr)
	require.EqualError(t, err, "--yes or -y flag is required when not running interactively")
	require.DirExists(t, stackDir(t, "a-stack"))
}

func TestDeleteWithoutStackNameRequiresPrompts(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("a-stack")
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmdDeleteStack, false)

	_, err = exec("--yes")

	var flagErr cmdutils.FlagError
	require.ErrorAs(t, err, &flagErr)
	require.EqualError(t, err, "the <stack-name> argument is required when prompts are disabled")
}

func TestDeleteWithUnknownStackName(t *testing.T) {
	git.InitGitRepo(t)
	_, err := git.AddStackRefDir("a-stack")
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmdDeleteStack, true)

	_, err = exec("not-a-stack --yes")

	require.EqualError(t, err, "deleting stack failed: no stack named \"not-a-stack\" found")
	require.DirExists(t, stackDir(t, "a-stack"))
}

func TestDeleteWithoutAnyStacks(t *testing.T) {
	git.InitGitRepo(t)

	exec := cmdtest.SetupCmdForTest(t, NewCmdDeleteStack, true)

	_, err := exec("--yes")

	require.EqualError(t, err, "deleting stack failed: no stacks found; create one with \"glab stack create\"")
}
