//go:build !integration

package update

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func setupExec(t *testing.T, tc *gitlabtesting.TestClient, opts ...cmdtest.FactoryOption) cmdtest.CmdExecFunc {
	t.Helper()

	opts = append(
		[]cmdtest.FactoryOption{
			cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		},
		opts...,
	)

	return cmdtest.SetupCmdForTest(t, NewCmd, false, opts...)
}

func noMorePages() *gitlab.Response {
	return &gitlab.Response{NextPage: 0}
}

// expectProjectUpdate returns an accessor for the options the command sent, so
// the tests can assert on the request and not only on what is printed.
func expectProjectUpdate(tc *gitlabtesting.TestClient, id int64, account *gitlab.ProjectServiceAccount) func() *gitlab.UpdateProjectServiceAccountOptions {
	var sent *gitlab.UpdateProjectServiceAccountOptions
	tc.MockProjects.EXPECT().
		UpdateProjectServiceAccount("OWNER/REPO", id, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, _ int64, opt *gitlab.UpdateProjectServiceAccountOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.ProjectServiceAccount, *gitlab.Response, error) {
			sent = opt
			return account, nil, nil
		})

	return func() *gitlab.UpdateProjectServiceAccountOptions { return sent }
}

// No list expectation is set: an ID goes straight to the update.
func TestUpdate_ProjectByID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectUpdate(tc, 51, &gitlab.ProjectServiceAccount{
		ID:       51,
		Username: "service_account_project_2_aaa",
		Name:     "Release bot",
		Email:    "deployer@example.com",
	})

	out, err := setupExec(t, tc)(`51 --name "Release bot"`)
	require.NoError(t, err)

	require.NotNil(t, sent().Name)
	assert.Equal(t, "Release bot", *sent().Name)
	assert.Nil(t, sent().Username)
	assert.Nil(t, sent().Email)

	expected := heredoc.Doc(`
		ID	51
		Username	service_account_project_2_aaa
		Name	Release bot
		Email	deployer@example.com
	`)
	assert.Equal(t, expected, out.OutBuf.String())
	assert.Equal(t, "✓ Updated service account 51 in project OWNER/REPO.\n", out.ErrBuf.String())
}

func TestUpdate_ByUsername(t *testing.T) {
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
			UpdateProjectServiceAccount("OWNER/REPO", int64(52), gomock.Any(), gomock.Any()).
			Return(&gitlab.ProjectServiceAccount{ID: 52, Username: "release-bot-2"}, nil, nil),
	)

	out, err := setupExec(t, tc)("release-bot --username release-bot-2")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "release-bot-2")
}

func TestUpdate_AllFields(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectUpdate(tc, 51, &gitlab.ProjectServiceAccount{
		ID:               51,
		Username:         "release-bot",
		Name:             "Release bot",
		Email:            "service_account_project_2_aaa@noreply.gitlab.example.com",
		UnconfirmedEmail: "release-bot@example.com",
	})

	out, err := setupExec(t, tc)(`51 --name "Release bot" --username release-bot --email release-bot@example.com`)
	require.NoError(t, err)

	assert.Equal(t, "Release bot", *sent().Name)
	assert.Equal(t, "release-bot", *sent().Username)
	assert.Equal(t, "release-bot@example.com", *sent().Email)

	expected := heredoc.Doc(`
		ID	51
		Username	release-bot
		Name	Release bot
		Email	service_account_project_2_aaa@noreply.gitlab.example.com
		Unconfirmed Email	release-bot@example.com
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

func TestUpdate_Group(t *testing.T) {
	t.Parallel()

	var sent *gitlab.UpdateServiceAccountOptions
	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		UpdateServiceAccount("group/sub-group", int64(41), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, _ int64, opt *gitlab.UpdateServiceAccountOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.GroupServiceAccount, *gitlab.Response, error) {
			sent = opt
			return &gitlab.GroupServiceAccount{ID: 41, UserName: "service_account_group_1_aaa", Email: "deployer@example.com"}, nil, nil
		})

	out, err := setupExec(t, tc)("41 --email deployer@example.com --group group/sub-group")
	require.NoError(t, err)

	assert.Nil(t, sent.Name)
	assert.Nil(t, sent.Username)
	assert.Equal(t, "deployer@example.com", *sent.Email)
	assert.Nil(t, sent.External)
	assert.Equal(t, "✓ Updated service account 41 in group group/sub-group.\n", out.ErrBuf.String())
}

func TestUpdate_JSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	expectProjectUpdate(tc, 51, &gitlab.ProjectServiceAccount{ID: 51, Username: "release-bot", Name: "Release bot"})

	out, err := setupExec(t, tc)(`51 --name "Release bot" --output json`)
	require.NoError(t, err)

	var result gitlab.ProjectServiceAccount
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	assert.Equal(t, int64(51), result.ID)
	assert.Equal(t, "Release bot", result.Name)
	assert.Empty(t, out.ErrBuf.String())
}

func TestUpdate_RequiresAField(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of the flags in the group [name username email] is required")
}

// No expectation is set, so the values are rejected before any API call.
func TestUpdate_RejectsEmptyValues(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		args string
		want string
	}{
		{args: `51 --name ""`, want: "--name can't be empty"},
		{args: `51 --username ""`, want: "--username can't be empty"},
		{args: `51 --email "  "`, want: "--email can't be empty"},
		{args: `51 --name "Release bot" --email ""`, want: "--email can't be empty"},
	} {
		t.Run(tt.args, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)

			_, err := setupExec(t, tc)(tt.args)
			require.Error(t, err)

			var flagErr *cmdutils.FlagError
			require.ErrorAs(t, err, &flagErr)
			assert.Equal(t, tt.want, err.Error())
		})
	}
}

func TestUpdate_UsernameNotFound(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.ProjectServiceAccount{}, noMorePages(), nil)

	_, err := setupExec(t, tc)(`missing --name "Release bot"`)
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to update service account", exitErr.Details)
	assert.Contains(t, err.Error(), `no service account with username "missing" in project OWNER/REPO`)
}

func TestUpdate_InvalidID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)(`0 --name "Release bot"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid service account ID "0"`)
}

func TestUpdate_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		UpdateProjectServiceAccount("OWNER/REPO", int64(51), gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("400 Bad Request"))

	_, err := setupExec(t, tc)("51 --email taken@example.com")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to update service account", exitErr.Details)
	assert.Contains(t, err.Error(), "400 Bad Request")
}

func TestUpdate_RequiresArgument(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)(`--name "Release bot"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 1 arg(s), received 0")
}

func TestUpdate_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)(`41 --name "Release bot" --repo OWNER/REPO --group my-group`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestUpdate_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))(`41 --name "Release bot"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

// GITLAB_GROUP redirects the command with no flag passed. t.Setenv rules out
// t.Parallel.
func TestUpdate_GroupFromEnvironment(t *testing.T) {
	t.Setenv("GITLAB_GROUP", "env-group")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		UpdateServiceAccount("env-group", int64(41), gomock.Any(), gomock.Any()).
		Return(&gitlab.GroupServiceAccount{ID: 41, UserName: "service_account_group_1_aaa"}, nil, nil)

	out, err := setupExec(t, tc)(`41 --name "Release bot"`)
	require.NoError(t, err)

	assert.Equal(t, "✓ Updated service account 41 in group env-group.\n", out.ErrBuf.String())
}
