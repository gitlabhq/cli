//go:build !integration

package get

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

func TestGet_Project(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		GetProjectServiceAccount("OWNER/REPO", int64(51), gomock.Any()).
		Return(&gitlab.ProjectServiceAccount{
			ID:       51,
			Username: "service_account_project_2_aaa",
			Name:     "Deployer",
			Email:    "deployer@example.com",
		}, nil, nil)

	out, err := setupExec(t, tc)("51")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	51
		Username	service_account_project_2_aaa
		Name	Deployer
		Email	deployer@example.com
	`)
	assert.Equal(t, expected, out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestGet_Group(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		GetServiceAccount("my-group", int64(41), gomock.Any()).
		Return(&gitlab.GroupServiceAccount{
			ID:       41,
			UserName: "service_account_group_1_aaa",
		}, nil, nil)

	out, err := setupExec(t, tc)("41 --group my-group")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	41
		Username	service_account_group_1_aaa
		Name	-
		Email	-
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

func TestGet_UnconfirmedEmail(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		GetServiceAccount("my-group", int64(41), gomock.Any()).
		Return(&gitlab.GroupServiceAccount{
			ID:               41,
			UserName:         "service_account_group_1_aaa",
			Email:            "service_account_group_1_aaa@noreply.gitlab.example.com",
			UnconfirmedEmail: "deployer@example.com",
		}, nil, nil)

	out, err := setupExec(t, tc)("41 --group my-group")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	41
		Username	service_account_group_1_aaa
		Name	-
		Email	service_account_group_1_aaa@noreply.gitlab.example.com
		Unconfirmed Email	deployer@example.com
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

// No GetProjectServiceAccount expectation is set: a username is answered from
// the list, so the command also works before GitLab 19.4.
func TestGet_ByUsername(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.ProjectServiceAccount{
			{ID: 51, Username: "service_account_project_2_aaa", Name: "Deployer"},
			{ID: 52, Username: "service_account_project_2_bbb", Name: "Releaser", Email: "releaser@example.com"},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc)("service_account_project_2_bbb")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	52
		Username	service_account_project_2_bbb
		Name	Releaser
		Email	releaser@example.com
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

func TestGet_ByUsernameJSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		ListServiceAccounts("my-group", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.GroupServiceAccount{
			{
				ID:          41,
				UserName:    "service_account_group_1_aaa",
				Name:        "Releaser",
				Email:       "releaser@example.com",
				PublicEmail: "public@example.com",
			},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc)("service_account_group_1_aaa --group my-group --output json")
	require.NoError(t, err)

	var result gitlab.GroupServiceAccount
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	assert.Equal(t, int64(41), result.ID)
	assert.Equal(t, "service_account_group_1_aaa", result.UserName)
	assert.Equal(t, "Releaser", result.Name)
	assert.Equal(t, "releaser@example.com", result.Email)
	assert.Equal(t, "public@example.com", result.PublicEmail)
}

func TestGet_UsernameNotFound(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		ListServiceAccounts("my-group", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.GroupServiceAccount{
			{ID: 41, UserName: "service_account_group_1_aaa"},
		}, noMorePages(), nil)

	_, err := setupExec(t, tc)("missing --group my-group")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to get service account", exitErr.Details)
	assert.Contains(t, err.Error(), `no service account with username "missing" in group my-group`)
}

// The group API type carries public_email, which the text output leaves out, so
// JSON output must print the response rather than the text fields.
func TestGet_JSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		GetServiceAccount("my-group", int64(41), gomock.Any()).
		Return(&gitlab.GroupServiceAccount{
			ID:          41,
			UserName:    "service_account_group_1_aaa",
			Name:        "Releaser",
			Email:       "releaser@example.com",
			PublicEmail: "public@example.com",
		}, nil, nil)

	out, err := setupExec(t, tc)("41 --group my-group --output json")
	require.NoError(t, err)

	var result gitlab.GroupServiceAccount
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	assert.Equal(t, int64(41), result.ID)
	assert.Equal(t, "service_account_group_1_aaa", result.UserName)
	assert.Equal(t, "Releaser", result.Name)
	assert.Equal(t, "releaser@example.com", result.Email)
	assert.Equal(t, "public@example.com", result.PublicEmail)
}

func TestGet_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		GetProjectServiceAccount("OWNER/REPO", int64(51), gomock.Any()).
		Return(nil, nil, errors.New("404 Not Found"))

	_, err := setupExec(t, tc)("51")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to get service account", exitErr.Details)
	assert.Contains(t, err.Error(), "404 Not Found")
}

func TestGet_InvalidID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("0")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to get service account", exitErr.Details)
	assert.Contains(t, err.Error(), `invalid service account ID "0"`)
}

func TestGet_RequiresArgument(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 1 arg(s), received 0")
}

func TestGet_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("41 --repo OWNER/REPO --group my-group")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestGet_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("41")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

func TestGet_OutsideRepositoryWithGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		GetServiceAccount("my-group", int64(41), gomock.Any()).
		Return(&gitlab.GroupServiceAccount{ID: 41, UserName: "service_account_group_1_aaa"}, nil, nil)

	out, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("41 --group my-group")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "service_account_group_1_aaa")
}

// GITLAB_GROUP redirects the command with no flag passed. t.Setenv rules out
// t.Parallel.
func TestGet_GroupFromEnvironment(t *testing.T) {
	t.Setenv("GITLAB_GROUP", "env-group")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		GetServiceAccount("env-group", int64(41), gomock.Any()).
		Return(&gitlab.GroupServiceAccount{ID: 41, UserName: "service_account_group_1_aaa"}, nil, nil)

	out, err := setupExec(t, tc)("41")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "service_account_group_1_aaa")
}
