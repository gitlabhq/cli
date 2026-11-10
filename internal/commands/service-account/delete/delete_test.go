//go:build !integration

package delete

import (
	"errors"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
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

func noMorePages() *gitlab.Response {
	return &gitlab.Response{NextPage: 0}
}

func TestDelete_Project(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		DeleteProjectServiceAccount("OWNER/REPO", int64(51), gomock.Cond(func(o *gitlab.DeleteProjectServiceAccountOptions) bool {
			return o.HardDelete == nil
		}), gomock.Any()).
		Return(nil, nil)

	out, err := setupExec(t, tc, false)("51 --yes")
	require.NoError(t, err)

	assert.Equal(t, "✓ Scheduled service account 51 of project OWNER/REPO for deletion.\n", out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestDelete_Group(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		DeleteServiceAccount("group/sub-group", int64(41), gomock.Cond(func(o *gitlab.DeleteServiceAccountOptions) bool {
			return o.HardDelete == nil
		}), gomock.Any()).
		Return(nil, nil)

	out, err := setupExec(t, tc, false)("41 --yes --group group/sub-group")
	require.NoError(t, err)

	assert.Equal(t, "✓ Scheduled service account 41 of group group/sub-group for deletion.\n", out.OutBuf.String())
}

func TestDelete_ByUsername(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	gomock.InOrder(
		tc.MockProjects.EXPECT().
			ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*gitlab.ProjectServiceAccount{
				{ID: 51, Username: "service_account_project_2_aaa"},
				{ID: 52, Username: "release-bot"},
			}, noMorePages(), nil),
		tc.MockProjects.EXPECT().
			DeleteProjectServiceAccount("OWNER/REPO", int64(52), gomock.Any(), gomock.Any()).
			Return(nil, nil),
	)

	out, err := setupExec(t, tc, false)("release-bot --yes")
	require.NoError(t, err)

	assert.Equal(t, "✓ Scheduled service account 52 of project OWNER/REPO for deletion.\n", out.OutBuf.String())
}

func TestDelete_RequiresYesWhenNotInteractive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, false)("51")
	require.Error(t, err)
	assert.Equal(t, "--yes required when not running interactively", err.Error())
}

func TestDelete_UsernameNotFound(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.ProjectServiceAccount{}, noMorePages(), nil)

	_, err := setupExec(t, tc, false)("missing --yes")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to delete service account", exitErr.Details)
	assert.Contains(t, err.Error(), `no service account with username "missing" in project OWNER/REPO`)
}

func TestDelete_InvalidID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, false)("0 --yes")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid service account ID "0"`)
}

func TestDelete_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		DeleteProjectServiceAccount("OWNER/REPO", int64(51), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("404 Not Found"))

	_, err := setupExec(t, tc, false)("51 --yes")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to delete service account", exitErr.Details)
	assert.Contains(t, err.Error(), "404 Not Found")
}

func TestDelete_RequiresArgument(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, false)("--yes")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 1 arg(s), received 0")
}

func TestDelete_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, false)("51 --yes --repo OWNER/REPO --group my-group")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestDelete_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, false, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("51 --yes")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

func TestDelete_OutsideRepositoryWithGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		DeleteServiceAccount("my-group", int64(41), gomock.Any(), gomock.Any()).
		Return(nil, nil)

	out, err := setupExec(t, tc, false, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("41 --yes --group my-group")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "Scheduled service account 41 of group my-group for deletion.")
}

func TestDelete_Prompt(t *testing.T) {
	// NOTE: This test cannot run in parallel because the huh form library
	// uses global state (charmbracelet/bubbles runeutil sanitizer).

	t.Run("confirming deletes", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			DeleteProjectServiceAccount("OWNER/REPO", int64(51), gomock.Any(), gomock.Any()).
			Return(nil, nil)

		c := ugh.New(t)
		c.Expect(ugh.Confirm("Are you ABSOLUTELY SURE you wish to delete service account 51?")).Do(ugh.Affirm)

		out, err := setupExec(t, tc, true, cmdtest.WithConsole(t, c))("51")
		require.NoError(t, err)

		assert.Contains(t, out.ErrBuf.String(), "This deletes service account 51 of project OWNER/REPO.")
		assert.Contains(t, out.OutBuf.String(), "Scheduled service account 51 of project OWNER/REPO for deletion.")
	})

	t.Run("declining deletes nothing", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			DeleteProjectServiceAccount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Times(0)

		c := ugh.New(t)
		c.Expect(ugh.Confirm("Are you ABSOLUTELY SURE you wish to delete service account 51?")).Do(ugh.Reject)

		_, err := setupExec(t, tc, true, cmdtest.WithConsole(t, c))("51")
		require.ErrorIs(t, err, iostreams.ErrUserCancelled)
	})

	// A username is resolved before the prompt, so the prompt names the ID that
	// is about to be deleted.
	t.Run("the prompt names the ID a username resolved to", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*gitlab.ProjectServiceAccount{{ID: 52, Username: "release-bot"}}, noMorePages(), nil)
		tc.MockProjects.EXPECT().
			DeleteProjectServiceAccount("OWNER/REPO", int64(52), gomock.Any(), gomock.Any()).
			Return(nil, nil)

		c := ugh.New(t)
		c.Expect(ugh.Confirm("Are you ABSOLUTELY SURE you wish to delete service account 52?")).Do(ugh.Affirm)

		_, err := setupExec(t, tc, true, cmdtest.WithConsole(t, c))("release-bot")
		require.NoError(t, err)
	})

	// GITLAB_GROUP redirects the command with no flag passed, so the advisory is
	// the only place the caller sees which group loses the account.
	t.Run("the advisory names a group from the environment", func(t *testing.T) {
		t.Setenv("GITLAB_GROUP", "env-group")

		tc := gitlabtesting.NewTestClient(t)
		tc.MockGroups.EXPECT().
			DeleteServiceAccount("env-group", int64(41), gomock.Any(), gomock.Any()).
			Return(nil, nil)

		c := ugh.New(t)
		c.Expect(ugh.Confirm("Are you ABSOLUTELY SURE you wish to delete service account 41?")).Do(ugh.Affirm)

		out, err := setupExec(t, tc, true, cmdtest.WithConsole(t, c))("41")
		require.NoError(t, err)

		assert.Contains(t, out.ErrBuf.String(), "This deletes service account 41 of group env-group.")
		assert.Contains(t, out.OutBuf.String(), "Scheduled service account 41 of group env-group for deletion.")
	})
}
