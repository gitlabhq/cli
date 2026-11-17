//go:build !integration

package list

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

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

func projectTokens() []*gitlab.PersonalAccessToken {
	expiresAt := gitlab.ISOTime(time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC))
	lastUsedAt := time.Date(2026, 11, 5, 9, 30, 0, 0, time.UTC)
	return []*gitlab.PersonalAccessToken{
		{ID: 7, Name: "ci", Scopes: []string{"api", "read_repository"}, Active: true, ExpiresAt: &expiresAt, LastUsedAt: &lastUsedAt},
		{ID: 8, Name: "old", Scopes: []string{"read_api"}, Active: false, Revoked: true},
	}
}

// No list expectation for accounts is set: an ID goes straight to the tokens.
func TestTokenList_Project(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Cond(func(o *gitlab.ListProjectServiceAccountPersonalAccessTokensOptions) bool {
			return o.State == nil && o.PerPage == 100
		}), gomock.Any(), gomock.Any()).
		Return(projectTokens(), noMorePages(), nil)

	out, err := setupExec(t, tc)("51")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	Name	Scopes	Active	Expires At	Last Used At
		7	ci	api, read_repository	true	2027-01-31	2026-11-05T09:30:00Z
		8	old	read_api	false	-	-
	`)
	assert.Equal(t, expected, out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestTokenList_Active(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Cond(func(o *gitlab.ListProjectServiceAccountPersonalAccessTokensOptions) bool {
			return o.State != nil && *o.State == "active"
		}), gomock.Any(), gomock.Any()).
		Return(projectTokens()[:1], noMorePages(), nil)

	out, err := setupExec(t, tc)("51 --active")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "ci")
	assert.NotContains(t, out.OutBuf.String(), "old")
}

func TestTokenList_Group(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		ListServiceAccountPersonalAccessTokens("my-group", int64(41), gomock.Cond(func(o *gitlab.ListServiceAccountPersonalAccessTokensOptions) bool {
			return o.State == nil && o.PerPage == 100
		}), gomock.Any(), gomock.Any()).
		Return([]*gitlab.PersonalAccessToken{{ID: 9, Name: "deploy", Scopes: []string{"api"}, Active: true}}, noMorePages(), nil)

	out, err := setupExec(t, tc)("41 --group my-group")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	Name	Scopes	Active	Expires At	Last Used At
		9	deploy	api	true	-	-
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

func TestTokenList_ByUsername(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	gomock.InOrder(
		tc.MockProjects.EXPECT().
			ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*gitlab.ProjectServiceAccount{{ID: 52, Username: "release-bot"}}, noMorePages(), nil),
		tc.MockProjects.EXPECT().
			ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(52), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(projectTokens(), noMorePages(), nil),
	)

	out, err := setupExec(t, tc)("release-bot")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "api, read_repository")
}

func TestTokenList_JSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(projectTokens(), noMorePages(), nil)

	out, err := setupExec(t, tc)("51 --output json")
	require.NoError(t, err)

	var result []*gitlab.PersonalAccessToken
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	require.Len(t, result, 2)
	assert.Equal(t, int64(7), result[0].ID)
	assert.Equal(t, "ci", result[0].Name)
	assert.Equal(t, []string{"api", "read_repository"}, result[0].Scopes)
	assert.True(t, result[0].Active)
	assert.Equal(t, int64(8), result[1].ID)
	assert.True(t, result[1].Revoked)
	assert.Empty(t, result[0].Token)
}

func TestTokenList_Empty(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.PersonalAccessToken{}, noMorePages(), nil)

	out, err := setupExec(t, tc)("51")
	require.NoError(t, err)

	assert.Equal(t, "No tokens found for service account 51.\n", out.OutBuf.String())
}

func TestTokenList_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("404 Not Found"))

	_, err := setupExec(t, tc)("51")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to list service account tokens", exitErr.Details)
	assert.Contains(t, err.Error(), "404 Not Found")
}

func TestTokenList_UsernameNotFound(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.ProjectServiceAccount{}, noMorePages(), nil)

	_, err := setupExec(t, tc)("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no service account with username "missing" in project OWNER/REPO`)
}

func TestTokenList_InvalidID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid service account ID "0"`)
}

func TestTokenList_RequiresArgument(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 1 arg(s), received 0")
}

func TestTokenList_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 --repo OWNER/REPO --group my-group")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestTokenList_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("51")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

func TestTokenList_OutsideRepositoryWithGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		ListServiceAccountPersonalAccessTokens("my-group", int64(41), gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.PersonalAccessToken{{ID: 9, Name: "deploy"}}, noMorePages(), nil)

	out, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("41 --group my-group")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "deploy")
}

// GITLAB_GROUP redirects the command with no flag passed. t.Setenv rules out
// t.Parallel.
func TestTokenList_GroupFromEnvironment(t *testing.T) {
	t.Setenv("GITLAB_GROUP", "env-group")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		ListServiceAccountPersonalAccessTokens("env-group", int64(41), gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.PersonalAccessToken{{ID: 9, Name: "deploy"}}, noMorePages(), nil)

	out, err := setupExec(t, tc)("41")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "deploy")
}
