//go:build !integration

package rotate

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
	"gitlab.com/gitlab-org/cli/internal/cmdutils/tokenduration"
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

func rotatedToken() *gitlab.PersonalAccessToken {
	expiresAt := gitlab.ISOTime(time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC))
	return &gitlab.PersonalAccessToken{
		ID:          12,
		Name:        "ci",
		Description: "CI token",
		Scopes:      []string{"api"},
		Active:      true,
		ExpiresAt:   &expiresAt,
		Token:       "glpat-new",
	}
}

// expectProjectRotate returns an accessor for the options the command sent, so
// the tests can assert on the request and not only on what is printed.
func expectProjectRotate(tc *gitlabtesting.TestClient) func() *gitlab.RotateProjectServiceAccountPersonalAccessTokenOptions {
	var sent *gitlab.RotateProjectServiceAccountPersonalAccessTokenOptions
	tc.MockProjects.EXPECT().
		RotateProjectServiceAccountPersonalAccessToken("OWNER/REPO", int64(51), int64(7), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, _, _ int64, opt *gitlab.RotateProjectServiceAccountPersonalAccessTokenOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.PersonalAccessToken, *gitlab.Response, error) {
			sent = opt
			return rotatedToken(), nil, nil
		})

	return func() *gitlab.RotateProjectServiceAccountPersonalAccessTokenOptions { return sent }
}

// No list expectation is set: both IDs go straight to the rotate.
func TestTokenRotate_ProjectByIDs(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	expectProjectRotate(tc)

	out, err := setupExec(t, tc)("51 7")
	require.NoError(t, err)

	assert.Equal(t, "glpat-new\n", out.OutBuf.String())
	expected := heredoc.Doc(`
		ID	12
		Name	ci
		Description	CI token
		Scopes	api
		Expires At	2027-01-31
		! Rotated token 7 of service account 51 in project OWNER/REPO into token 12. Store the token value now. It is not shown again.
	`)
	assert.Equal(t, expected, out.ErrBuf.String())
}

func TestTokenRotate_DefaultExpiryIs30Days(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectRotate(tc)

	_, err := setupExec(t, tc)("51 7")
	require.NoError(t, err)

	want := tokenduration.TokenDuration(30 * 24 * time.Hour).CalculateExpirationDate().Format(time.DateOnly)
	require.NotNil(t, sent().ExpiresAt)
	assert.Equal(t, want, sent().ExpiresAt.String())
}

func TestTokenRotate_Duration(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectRotate(tc)

	_, err := setupExec(t, tc)("51 7 --duration 2w")
	require.NoError(t, err)

	want := tokenduration.TokenDuration(14 * 24 * time.Hour).CalculateExpirationDate().Format(time.DateOnly)
	assert.Equal(t, want, sent().ExpiresAt.String())
}

func TestTokenRotate_ExpiresAt(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectRotate(tc)

	_, err := setupExec(t, tc)("51 7 --expires-at 2027-12-31")
	require.NoError(t, err)

	assert.Equal(t, "2027-12-31", sent().ExpiresAt.String())
}

func TestTokenRotate_Group(t *testing.T) {
	t.Parallel()

	var sent *gitlab.RotateServiceAccountPersonalAccessTokenOptions
	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		RotateServiceAccountPersonalAccessToken("group/sub-group", int64(41), int64(9), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, _, _ int64, opt *gitlab.RotateServiceAccountPersonalAccessTokenOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.PersonalAccessToken, *gitlab.Response, error) {
			sent = opt
			return rotatedToken(), nil, nil
		})

	out, err := setupExec(t, tc)("41 9 --expires-at 2027-12-31 --group group/sub-group")
	require.NoError(t, err)

	assert.Equal(t, "2027-12-31", sent.ExpiresAt.String())
	assert.Contains(t, out.ErrBuf.String(), "! Rotated token 9 of service account 41 in group group/sub-group into token 12. Store the token value now. It is not shown again.\n")
}

func TestTokenRotate_ByNames(t *testing.T) {
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
			Return([]*gitlab.PersonalAccessToken{{ID: 7, Name: "ci"}}, noMorePages(), nil),
		tc.MockProjects.EXPECT().
			RotateProjectServiceAccountPersonalAccessToken("OWNER/REPO", int64(52), int64(7), gomock.Any(), gomock.Any()).
			Return(rotatedToken(), nil, nil),
	)

	out, err := setupExec(t, tc)("release-bot ci")
	require.NoError(t, err)

	assert.Contains(t, out.ErrBuf.String(), "Rotated token 7 of service account 52 in project OWNER/REPO into token 12.")
}

// Like create, rotate prints the new token value, because this is the only
// time GitLab returns it.
func TestTokenRotate_JSONKeepsTokenValue(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	expectProjectRotate(tc)

	out, err := setupExec(t, tc)("51 7 --output json")
	require.NoError(t, err)

	var result gitlab.PersonalAccessToken
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	assert.Equal(t, int64(12), result.ID)
	assert.Equal(t, "ci", result.Name)
	assert.Equal(t, "glpat-new", result.Token)
	assert.Empty(t, out.ErrBuf.String())
}

func TestTokenRotate_DurationAndExpiresAtAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 7 --duration 7d --expires-at 2027-12-31")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestTokenRotate_InvalidDuration(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 7 --duration 400d")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duration must be between 1 and 365 days")
}

func TestTokenRotate_InvalidExpiresAt(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 7 --expires-at 31.12.2027")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid argument "31.12.2027" for "-E, --expires-at"`)
}

func TestTokenRotate_TokenNameNotFound(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.PersonalAccessToken{}, noMorePages(), nil)

	_, err := setupExec(t, tc)("51 ci")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to rotate service account token", exitErr.Details)
	assert.Contains(t, err.Error(), `no active token named "ci" for service account 51 in project OWNER/REPO`)
}

func TestTokenRotate_DuplicateTokenName(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.PersonalAccessToken{{ID: 7, Name: "ci"}, {ID: 8, Name: "ci"}}, noMorePages(), nil)

	_, err := setupExec(t, tc)("51 ci")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `multiple active tokens named "ci" for service account 51; use the token ID instead`)
}

func TestTokenRotate_InvalidTokenID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid token ID "0"`)
}

func TestTokenRotate_InvalidAccountID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("0 7")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid service account ID "0"`)
}

func TestTokenRotate_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		RotateProjectServiceAccountPersonalAccessToken("OWNER/REPO", int64(51), int64(7), gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("400 Bad Request"))

	_, err := setupExec(t, tc)("51 7")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to rotate service account token", exitErr.Details)
	assert.Contains(t, err.Error(), "400 Bad Request")
}

func TestTokenRotate_RequiresArguments(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 2 arg(s), received 1")
}

func TestTokenRotate_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 7 --repo OWNER/REPO --group my-group")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestTokenRotate_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("51 7")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

func TestTokenRotate_OutsideRepositoryWithGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		RotateServiceAccountPersonalAccessToken("my-group", int64(41), int64(9), gomock.Any(), gomock.Any()).
		Return(rotatedToken(), nil, nil)

	out, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("41 9 --group my-group")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "glpat-new")
}

// GITLAB_GROUP redirects the command with no flag passed. t.Setenv rules out
// t.Parallel.
func TestTokenRotate_GroupFromEnvironment(t *testing.T) {
	t.Setenv("GITLAB_GROUP", "env-group")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		RotateServiceAccountPersonalAccessToken("env-group", int64(41), int64(9), gomock.Any(), gomock.Any()).
		Return(rotatedToken(), nil, nil)

	out, err := setupExec(t, tc)("41 9")
	require.NoError(t, err)

	assert.Contains(t, out.ErrBuf.String(), "in group env-group into token 12.")
}
