//go:build !integration

package delete

import (
	"errors"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func setupExec(t *testing.T, tc *gitlabtesting.TestClient, isTTY bool, opts ...cmdtest.FactoryOption) cmdtest.CmdExecFunc {
	t.Helper()

	opts = append(
		[]cmdtest.FactoryOption{
			cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		},
		opts...,
	)

	return cmdtest.SetupCmdForTest(t, NewCmd, isTTY, opts...)
}

func TestDelete_Project(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		DeleteProjectDeployToken("OWNER/REPO", int64(42), gomock.Any()).
		Return(nil, nil)

	out, err := setupExec(t, tc, false)("42 --yes")
	require.NoError(t, err)

	assert.Equal(t, "✓ Deleted deploy token 42 of project OWNER/REPO.\n", out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestDelete_Group(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		DeleteGroupDeployToken("group/sub-group", int64(7), gomock.Any()).
		Return(nil, nil)

	out, err := setupExec(t, tc, false)("7 --yes --group group/sub-group")
	require.NoError(t, err)

	assert.Equal(t, "✓ Deleted deploy token 7 of group group/sub-group.\n", out.OutBuf.String())
}

func TestDelete_RequiresYesWhenNotInteractive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, false)("42")
	require.Error(t, err)
	assert.Equal(t, "--yes required when not running interactively", err.Error())
}

func TestDelete_InvalidTokenID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, false)("not-a-number --yes")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid deploy token ID: "not-a-number"`)
}

func TestDelete_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		DeleteProjectDeployToken("OWNER/REPO", int64(42), gomock.Any()).
		Return(nil, errors.New("404 Not Found"))

	_, err := setupExec(t, tc, false)("42 --yes")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to delete deploy token", exitErr.Details)
	assert.Contains(t, err.Error(), "404 Not Found")
}

func TestDelete_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, false)("42 --yes --repo OWNER/REPO --group group/sub-group")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

// The project scope needs a base repository, and says so before any client is built.
func TestDelete_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, false, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("42 --yes")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

// The group scope does not need a base repository.
func TestDelete_OutsideRepositoryWithGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		DeleteGroupDeployToken("group/sub-group", int64(7), gomock.Any()).
		Return(nil, nil)

	out, err := setupExec(t, tc, false, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("7 --yes --group group/sub-group")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "Deleted deploy token 7 of group group/sub-group.")
}

func TestDelete_Prompt(t *testing.T) {
	// NOTE: This test cannot run in parallel because the huh form library
	// uses global state (charmbracelet/bubbles runeutil sanitizer).

	t.Run("confirming deletes", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)
		tc.MockDeployTokens.EXPECT().
			DeleteProjectDeployToken("OWNER/REPO", int64(42), gomock.Any()).
			Return(nil, nil)

		c := ugh.New(t)
		c.Expect(ugh.Confirm("Are you ABSOLUTELY SURE you wish to delete deploy token 42?")).Do(ugh.Affirm)

		out, err := setupExec(t, tc, true, cmdtest.WithConsole(t, c))("42")
		require.NoError(t, err)

		assert.Contains(t, out.ErrBuf.String(), "This permanently deletes deploy token 42 of project OWNER/REPO.")
		assert.Contains(t, out.OutBuf.String(), "Deleted deploy token 42 of project OWNER/REPO.")
	})

	t.Run("declining deletes nothing", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)
		tc.MockDeployTokens.EXPECT().
			DeleteProjectDeployToken(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(0)

		c := ugh.New(t)
		c.Expect(ugh.Confirm("Are you ABSOLUTELY SURE you wish to delete deploy token 42?")).Do(ugh.Reject)

		_, err := setupExec(t, tc, true, cmdtest.WithConsole(t, c))("42")
		require.ErrorIs(t, err, iostreams.ErrUserCancelled)
	})

	// GITLAB_GROUP redirects the command with no flag passed, so the advisory is
	// the only place the caller sees which target loses a token.
	t.Run("the advisory names a group from the environment", func(t *testing.T) {
		t.Setenv("GITLAB_GROUP", "env-group")

		tc := gitlabtesting.NewTestClient(t)
		tc.MockDeployTokens.EXPECT().
			DeleteGroupDeployToken("env-group", int64(7), gomock.Any()).
			Return(nil, nil)

		c := ugh.New(t)
		c.Expect(ugh.Confirm("Are you ABSOLUTELY SURE you wish to delete deploy token 7?")).Do(ugh.Affirm)

		out, err := setupExec(t, tc, true, cmdtest.WithConsole(t, c))("7")
		require.NoError(t, err)

		assert.Contains(t, out.ErrBuf.String(), "This permanently deletes deploy token 7 of group env-group.")
		assert.Contains(t, out.OutBuf.String(), "Deleted deploy token 7 of group env-group.")
	})
}
