//go:build !integration

package create

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

func createdToken() *gitlab.PersonalAccessToken {
	expiresAt := gitlab.ISOTime(time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC))
	return &gitlab.PersonalAccessToken{
		ID:        7,
		Name:      "ci",
		Scopes:    []string{"api"},
		Active:    true,
		ExpiresAt: &expiresAt,
		Token:     "glpat-secret",
	}
}

// expectProjectCreate returns an accessor for the options the command sent, so
// the tests can assert on the request and not only on what is printed.
func expectProjectCreate(tc *gitlabtesting.TestClient) func() *gitlab.CreateProjectServiceAccountPersonalAccessTokenOptions {
	var sent *gitlab.CreateProjectServiceAccountPersonalAccessTokenOptions
	tc.MockProjects.EXPECT().
		CreateProjectServiceAccountPersonalAccessToken("OWNER/REPO", int64(51), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, _ int64, opt *gitlab.CreateProjectServiceAccountPersonalAccessTokenOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.PersonalAccessToken, *gitlab.Response, error) {
			sent = opt
			return createdToken(), nil, nil
		})

	return func() *gitlab.CreateProjectServiceAccountPersonalAccessTokenOptions { return sent }
}

// No account list expectation is set: an ID goes straight to the create.
func TestTokenCreate_Project(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectCreate(tc)

	out, err := setupExec(t, tc)("51 ci --scope api")
	require.NoError(t, err)

	assert.Equal(t, "ci", *sent().Name)
	assert.Equal(t, []string{"api"}, *sent().Scopes)
	assert.Nil(t, sent().Description)

	assert.Equal(t, "glpat-secret\n", out.OutBuf.String())
	expected := heredoc.Doc(`
		ID	7
		Name	ci
		Description	-
		Scopes	api
		Expires At	2027-01-31
		! Created token 7 for service account 51 in project OWNER/REPO. Store the token value now. It is not shown again.
	`)
	assert.Equal(t, expected, out.ErrBuf.String())
}

func TestTokenCreate_DefaultExpiryIs30Days(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectCreate(tc)

	_, err := setupExec(t, tc)("51 ci --scope api")
	require.NoError(t, err)

	want := tokenduration.TokenDuration(30 * 24 * time.Hour).CalculateExpirationDate().Format(time.DateOnly)
	require.NotNil(t, sent().ExpiresAt)
	assert.Equal(t, want, sent().ExpiresAt.String())
}

func TestTokenCreate_Duration(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectCreate(tc)

	_, err := setupExec(t, tc)("51 ci --scope api --duration 7d")
	require.NoError(t, err)

	want := tokenduration.TokenDuration(7 * 24 * time.Hour).CalculateExpirationDate().Format(time.DateOnly)
	assert.Equal(t, want, sent().ExpiresAt.String())
}

func TestTokenCreate_ExpiresAt(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectCreate(tc)

	_, err := setupExec(t, tc)("51 ci --scope api --expires-at 2027-12-31")
	require.NoError(t, err)

	assert.Equal(t, "2027-12-31", sent().ExpiresAt.String())
}

func TestTokenCreate_ScopesAndDescription(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectCreate(tc)

	_, err := setupExec(t, tc)(`51 ci --scope read_api,read_repository --scope read_registry --description "CI reader"`)
	require.NoError(t, err)

	assert.Equal(t, []string{"read_api", "read_repository", "read_registry"}, *sent().Scopes)
	require.NotNil(t, sent().Description)
	assert.Equal(t, "CI reader", *sent().Description)
}

func TestTokenCreate_Group(t *testing.T) {
	t.Parallel()

	var sent *gitlab.CreateServiceAccountPersonalAccessTokenOptions
	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		CreateServiceAccountPersonalAccessToken("group/sub-group", int64(41), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, _ int64, opt *gitlab.CreateServiceAccountPersonalAccessTokenOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.PersonalAccessToken, *gitlab.Response, error) {
			sent = opt
			return createdToken(), nil, nil
		})

	out, err := setupExec(t, tc)("41 ci --scope api --expires-at 2027-12-31 --group group/sub-group")
	require.NoError(t, err)

	assert.Equal(t, "ci", *sent.Name)
	assert.Equal(t, []string{"api"}, *sent.Scopes)
	assert.Equal(t, "2027-12-31", sent.ExpiresAt.String())
	assert.Contains(t, out.ErrBuf.String(), "! Created token 7 for service account 41 in group group/sub-group. Store the token value now. It is not shown again.\n")
}

func TestTokenCreate_ByUsername(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	gomock.InOrder(
		tc.MockProjects.EXPECT().
			ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*gitlab.ProjectServiceAccount{{ID: 52, Username: "release-bot"}}, noMorePages(), nil),
		tc.MockProjects.EXPECT().
			CreateProjectServiceAccountPersonalAccessToken("OWNER/REPO", int64(52), gomock.Any(), gomock.Any()).
			Return(createdToken(), nil, nil),
	)

	out, err := setupExec(t, tc)("release-bot ci --scope api")
	require.NoError(t, err)

	assert.Contains(t, out.ErrBuf.String(), "for service account 52 in project OWNER/REPO")
}

// Unlike token list, create prints the token value, because this is the only
// time GitLab returns it.
func TestTokenCreate_JSONKeepsTokenValue(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	expectProjectCreate(tc)

	out, err := setupExec(t, tc)("51 ci --scope api --output json")
	require.NoError(t, err)

	var result gitlab.PersonalAccessToken
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	assert.Equal(t, int64(7), result.ID)
	assert.Equal(t, "ci", result.Name)
	assert.Equal(t, "glpat-secret", result.Token)
	assert.Empty(t, out.ErrBuf.String())
}

func TestTokenCreate_ScopeIsRequired(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 ci")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `required flag(s) "scope" not set`)
}

func TestTokenCreate_DurationAndExpiresAtAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 ci --scope api --duration 7d --expires-at 2027-12-31")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestTokenCreate_InvalidDuration(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 ci --scope api --duration 400d")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duration must be between 1 and 365 days")
}

func TestTokenCreate_InvalidExpiresAt(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 ci --scope api --expires-at 31.12.2027")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid argument "31.12.2027" for "-E, --expires-at"`)
}

func TestTokenCreate_InvalidAccountID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("0 ci --scope api")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid service account ID "0"`)
}

func TestTokenCreate_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		CreateProjectServiceAccountPersonalAccessToken("OWNER/REPO", int64(51), gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("400 Bad Request"))

	_, err := setupExec(t, tc)("51 ci --scope api")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to create service account token", exitErr.Details)
	assert.Contains(t, err.Error(), "400 Bad Request")
}

func TestTokenCreate_RequiresArguments(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 --scope api")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 2 arg(s), received 1")
}

func TestTokenCreate_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("51 ci --scope api --repo OWNER/REPO --group my-group")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestTokenCreate_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("51 ci --scope api")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

func TestTokenCreate_OutsideRepositoryWithGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		CreateServiceAccountPersonalAccessToken("my-group", int64(41), gomock.Any(), gomock.Any()).
		Return(createdToken(), nil, nil)

	out, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("41 ci --scope api --group my-group")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "glpat-secret")
}

// GITLAB_GROUP redirects the command with no flag passed. t.Setenv rules out
// t.Parallel.
func TestTokenCreate_GroupFromEnvironment(t *testing.T) {
	t.Setenv("GITLAB_GROUP", "env-group")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockGroups.EXPECT().
		CreateServiceAccountPersonalAccessToken("env-group", int64(41), gomock.Any(), gomock.Any()).
		Return(createdToken(), nil, nil)

	out, err := setupExec(t, tc)("41 ci --scope api")
	require.NoError(t, err)

	assert.Contains(t, out.ErrBuf.String(), "in group env-group.")
}
