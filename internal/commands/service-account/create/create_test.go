//go:build !integration

package create

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

// expectProjectCreate returns an accessor for the options the command sent, so
// the tests can assert on the request and not only on what is printed.
func expectProjectCreate(tc *gitlabtesting.TestClient, account *gitlab.ProjectServiceAccount) func() *gitlab.CreateProjectServiceAccountOptions {
	var sent *gitlab.CreateProjectServiceAccountOptions
	tc.MockProjects.EXPECT().
		CreateProjectServiceAccount("OWNER/REPO", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, opt *gitlab.CreateProjectServiceAccountOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.ProjectServiceAccount, *gitlab.Response, error) {
			sent = opt
			return account, nil, nil
		})

	return func() *gitlab.CreateProjectServiceAccountOptions { return sent }
}

func TestCreate_ProjectWithDefaults(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectCreate(tc, &gitlab.ProjectServiceAccount{
		ID:       61,
		Username: "service_account_project_2_ccc",
		Name:     "Service account user",
		Email:    "service_account_project_2_ccc@noreply.gitlab.example.com",
	})

	out, err := setupExec(t, tc)("")
	require.NoError(t, err)

	assert.Nil(t, sent().Name)
	assert.Nil(t, sent().Username)
	assert.Nil(t, sent().Email)

	expected := heredoc.Doc(`
		ID	61
		Username	service_account_project_2_ccc
		Name	Service account user
		Email	service_account_project_2_ccc@noreply.gitlab.example.com
	`)
	assert.Equal(t, expected, out.OutBuf.String())
	assert.Equal(t, "✓ Created service account 61 in project OWNER/REPO.\n", out.ErrBuf.String())
}

func TestCreate_SendsOnlyGivenFlags(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectCreate(tc, &gitlab.ProjectServiceAccount{ID: 61, Username: "service_account_project_2_ccc", Name: "Release bot"})

	_, err := setupExec(t, tc)(`--name "Release bot"`)
	require.NoError(t, err)

	require.NotNil(t, sent().Name)
	assert.Equal(t, "Release bot", *sent().Name)
	assert.Nil(t, sent().Username)
	assert.Nil(t, sent().Email)
}

func TestCreate_AllFlags(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectCreate(tc, &gitlab.ProjectServiceAccount{
		ID:               61,
		Username:         "release-bot",
		Name:             "Release bot",
		Email:            "service_account_project_2_ccc@noreply.gitlab.example.com",
		UnconfirmedEmail: "release-bot@example.com",
	})

	out, err := setupExec(t, tc)(`--name "Release bot" --username release-bot --email release-bot@example.com`)
	require.NoError(t, err)

	assert.Equal(t, "Release bot", *sent().Name)
	assert.Equal(t, "release-bot", *sent().Username)
	assert.Equal(t, "release-bot@example.com", *sent().Email)

	expected := heredoc.Doc(`
		ID	61
		Username	release-bot
		Name	Release bot
		Email	service_account_project_2_ccc@noreply.gitlab.example.com
		Unconfirmed Email	release-bot@example.com
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

func TestCreate_Group(t *testing.T) {
	t.Parallel()

	var sent *gitlab.CreateServiceAccountOptions
	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		CreateServiceAccount("group/sub-group", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, opt *gitlab.CreateServiceAccountOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.GroupServiceAccount, *gitlab.Response, error) {
			sent = opt
			return &gitlab.GroupServiceAccount{ID: 71, UserName: "release-bot", Name: "Release bot"}, nil, nil
		})

	out, err := setupExec(t, tc)(`--name "Release bot" --username release-bot --group group/sub-group`)
	require.NoError(t, err)

	assert.Equal(t, "Release bot", *sent.Name)
	assert.Equal(t, "release-bot", *sent.Username)
	assert.Nil(t, sent.Email)

	expected := heredoc.Doc(`
		ID	71
		Username	release-bot
		Name	Release bot
		Email	-
	`)
	assert.Equal(t, expected, out.OutBuf.String())
	assert.Equal(t, "✓ Created service account 71 in group group/sub-group.\n", out.ErrBuf.String())
}

func TestCreate_JSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	expectProjectCreate(tc, &gitlab.ProjectServiceAccount{
		ID:               61,
		Username:         "release-bot",
		Name:             "Release bot",
		UnconfirmedEmail: "release-bot@example.com",
	})

	out, err := setupExec(t, tc)(`--name "Release bot" --output json`)
	require.NoError(t, err)

	var result gitlab.ProjectServiceAccount
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	assert.Equal(t, int64(61), result.ID)
	assert.Equal(t, "release-bot", result.Username)
	assert.Equal(t, "release-bot@example.com", result.UnconfirmedEmail)
	assert.Empty(t, out.ErrBuf.String())
}

func TestCreate_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		CreateProjectServiceAccount("OWNER/REPO", gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("400 Bad Request"))

	_, err := setupExec(t, tc)("--email taken@example.com")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to create service account", exitErr.Details)
	assert.Contains(t, err.Error(), "400 Bad Request")
}

func TestCreate_RejectsArguments(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("release-bot")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown command "release-bot"`)
}

func TestCreate_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("--repo OWNER/REPO --group my-group")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestCreate_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

func TestCreate_OutsideRepositoryWithGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		CreateServiceAccount("my-group", gomock.Any(), gomock.Any()).
		Return(&gitlab.GroupServiceAccount{ID: 71, UserName: "service_account_group_1_ccc"}, nil, nil)

	out, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("--group my-group")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "service_account_group_1_ccc")
}

// GITLAB_GROUP redirects the command with no flag passed. t.Setenv rules out
// t.Parallel.
func TestCreate_GroupFromEnvironment(t *testing.T) {
	t.Setenv("GITLAB_GROUP", "env-group")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		CreateServiceAccount("env-group", gomock.Any(), gomock.Any()).
		Return(&gitlab.GroupServiceAccount{ID: 71, UserName: "service_account_group_1_ccc"}, nil, nil)

	out, err := setupExec(t, tc)("")
	require.NoError(t, err)

	assert.Equal(t, "✓ Created service account 71 in group env-group.\n", out.ErrBuf.String())
}
