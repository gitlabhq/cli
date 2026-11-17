//go:build !integration

package revoke

import (
	"errors"
	"testing"

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

// No list expectation is set: both IDs go straight to the revoke.
func TestTokenRevoke_ProjectByIDs(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		RevokeProjectServiceAccountPersonalAccessToken("OWNER/REPO", int64(51), int64(7), gomock.Any()).
		Return(nil, nil)

	out, err := setupExec(t, tc)("51 7")
	require.NoError(t, err)

	assert.Equal(t, "✓ Revoked token 7 of service account 51 in project OWNER/REPO.\n", out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestTokenRevoke_Group(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		RevokeServiceAccountPersonalAccessToken("group/sub-group", int64(41), int64(9), gomock.Any()).
		Return(nil, nil)

	out, err := setupExec(t, tc)("41 9 --group group/sub-group")
	require.NoError(t, err)

	assert.Equal(t, "✓ Revoked token 9 of service account 41 in group group/sub-group.\n", out.OutBuf.String())
}

func TestTokenRevoke_ByNames(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	gomock.InOrder(
		tc.MockProjects.EXPECT().
			ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*gitlab.ProjectServiceAccount{{ID: 52, Username: "release-bot"}}, noMorePages(), nil),
		tc.MockProjects.EXPECT().
			ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(52), gomock.Cond(func(o *gitlab.ListProjectServiceAccountPersonalAccessTokensOptions) bool {
				return o.State != nil && *o.State == "active" && o.Search != nil && *o.Search == "ci"
			}), gomock.Any(), gomock.Any()).
			Return([]*gitlab.PersonalAccessToken{{ID: 6, Name: "ci-old"}, {ID: 7, Name: "ci"}}, noMorePages(), nil),
		tc.MockProjects.EXPECT().
			RevokeProjectServiceAccountPersonalAccessToken("OWNER/REPO", int64(52), int64(7), gomock.Any()).
			Return(nil, nil),
	)

	out, err := setupExec(t, tc)("release-bot ci")
	require.NoError(t, err)

	assert.Equal(t, "✓ Revoked token 7 of service account 52 in project OWNER/REPO.\n", out.OutBuf.String())
}

func TestTokenRevoke_TokenNameNotFound(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.PersonalAccessToken{}, noMorePages(), nil)

	_, err := setupExec(t, tc)("51 ci")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to revoke service account token", exitErr.Details)
	assert.Contains(t, err.Error(), `no active token named "ci" for service account 51 in project OWNER/REPO`)
}

func TestTokenRevoke_DuplicateTokenName(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.PersonalAccessToken{{ID: 7, Name: "ci"}, {ID: 8, Name: "ci"}}, noMorePages(), nil)

	_, err := setupExec(t, tc)("51 ci")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `multiple active tokens named "ci" for service account 51; use the token ID instead`)
}

func TestTokenRevoke_InvalidTokenID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid token ID "0"`)
}

func TestTokenRevoke_InvalidAccountID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("0 7")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid service account ID "0"`)
}

func TestTokenRevoke_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		RevokeProjectServiceAccountPersonalAccessToken("OWNER/REPO", int64(51), int64(7), gomock.Any()).
		Return(nil, errors.New("404 Not Found"))

	_, err := setupExec(t, tc)("51 7")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to revoke service account token", exitErr.Details)
	assert.Contains(t, err.Error(), "404 Not Found")
}

func TestTokenRevoke_RequiresArguments(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 2 arg(s), received 1")
}

func TestTokenRevoke_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 7 --repo OWNER/REPO --group my-group")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestTokenRevoke_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("51 7")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

func TestTokenRevoke_OutsideRepositoryWithGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		RevokeServiceAccountPersonalAccessToken("my-group", int64(41), int64(9), gomock.Any()).
		Return(nil, nil)

	out, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("41 9 --group my-group")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "Revoked token 9 of service account 41 in group my-group.")
}

// GITLAB_GROUP redirects the command with no flag passed. t.Setenv rules out
// t.Parallel.
func TestTokenRevoke_GroupFromEnvironment(t *testing.T) {
	t.Setenv("GITLAB_GROUP", "env-group")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		RevokeServiceAccountPersonalAccessToken("env-group", int64(41), int64(9), gomock.Any()).
		Return(nil, nil)

	out, err := setupExec(t, tc)("41 9")
	require.NoError(t, err)

	assert.Equal(t, "✓ Revoked token 9 of service account 41 in group env-group.\n", out.OutBuf.String())
}
