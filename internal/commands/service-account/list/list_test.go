//go:build !integration

package list

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

func TestList_Project(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.ProjectServiceAccount{
			{ID: 51, Username: "service_account_project_2_aaa", Name: "Deployer", Email: "deployer@example.com"},
			{ID: 52, Username: "service_account_project_2_bbb"},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc)("")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	Username	Name	Email
		51	service_account_project_2_aaa	Deployer	deployer@example.com
		52	service_account_project_2_bbb	-	-
	`)
	assert.Equal(t, expected, out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestList_Group(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		ListServiceAccounts("my-group", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.GroupServiceAccount{
			{ID: 41, UserName: "service_account_group_1_aaa", Name: "Releaser", Email: "releaser@example.com"},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc)("--group my-group")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	Username	Name	Email
		41	service_account_group_1_aaa	Releaser	releaser@example.com
	`)
	assert.Equal(t, expected, out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestList_Requests100PerPage(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccounts("OWNER/REPO", gomock.Cond(func(o *gitlab.ListProjectServiceAccountsOptions) bool { return o.PerPage == 100 }), gomock.Any(), gomock.Any()).
		Return([]*gitlab.ProjectServiceAccount{}, noMorePages(), nil)

	_, err := setupExec(t, tc)("")
	require.NoError(t, err)
}

func TestList_ProjectJSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.ProjectServiceAccount{
			{ID: 51, Username: "service_account_project_2_aaa", Name: "Deployer", Email: "deployer@example.com"},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc)("--output json")
	require.NoError(t, err)

	var result []*gitlab.ProjectServiceAccount
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, int64(51), result[0].ID)
	assert.Equal(t, "service_account_project_2_aaa", result[0].Username)
	assert.Equal(t, "Deployer", result[0].Name)
	assert.Equal(t, "deployer@example.com", result[0].Email)
}

// The group API type carries public_email, which the text table leaves out, so
// JSON output must print the response rather than the table's columns.
func TestList_GroupJSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		ListServiceAccounts("my-group", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.GroupServiceAccount{
			{ID: 41, UserName: "service_account_group_1_aaa", Name: "Releaser", Email: "releaser@example.com", PublicEmail: "public@example.com"},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc)("--group my-group --output json")
	require.NoError(t, err)

	var result []*gitlab.GroupServiceAccount
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, int64(41), result[0].ID)
	assert.Equal(t, "service_account_group_1_aaa", result[0].UserName)
	assert.Equal(t, "Releaser", result[0].Name)
	assert.Equal(t, "releaser@example.com", result[0].Email)
	assert.Equal(t, "public@example.com", result[0].PublicEmail)
}

func TestList_Empty(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.ProjectServiceAccount{}, noMorePages(), nil)

	out, err := setupExec(t, tc)("")
	require.NoError(t, err)

	assert.Equal(t, "No service accounts found.\n", out.OutBuf.String())
}

func TestList_EmptyJSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, noMorePages(), nil)

	out, err := setupExec(t, tc)("--output json")
	require.NoError(t, err)

	assert.Equal(t, "[]\n", out.OutBuf.String())
}

func TestList_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("403 Forbidden"))

	_, err := setupExec(t, tc)("")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to list service accounts", exitErr.Details)
	assert.Contains(t, err.Error(), "403 Forbidden")
}

func TestList_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("--repo OWNER/REPO --group my-group")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestList_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

func TestList_OutsideRepositoryWithGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		ListServiceAccounts("my-group", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.GroupServiceAccount{
			{ID: 41, UserName: "service_account_group_1_aaa", Name: "Releaser"},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("--group my-group")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "service_account_group_1_aaa")
}

// GITLAB_GROUP redirects the command with no flag passed. t.Setenv rules out
// t.Parallel.
func TestList_GroupFromEnvironment(t *testing.T) {
	t.Setenv("GITLAB_GROUP", "env-group")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		ListServiceAccounts("env-group", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.GroupServiceAccount{
			{ID: 41, UserName: "service_account_group_1_aaa", Name: "Releaser"},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc)("")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "service_account_group_1_aaa")
}
