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

func createdToken() *gitlab.DeployToken {
	return &gitlab.DeployToken{
		ID:       42,
		Name:     "ci-reader",
		Username: "gitlab+deploy-token-42",
		Scopes:   []string{"read_repository"},
		Token:    "gldt-secret",
	}
}

// expectProjectCreate returns an accessor for the options the command sent, so
// the tests can assert on the request and not only on what is printed.
func expectProjectCreate(tc *gitlabtesting.TestClient, token *gitlab.DeployToken) func() *gitlab.CreateProjectDeployTokenOptions {
	var sent *gitlab.CreateProjectDeployTokenOptions
	tc.MockDeployTokens.EXPECT().
		CreateProjectDeployToken("OWNER/REPO", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, opt *gitlab.CreateProjectDeployTokenOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DeployToken, *gitlab.Response, error) {
			sent = opt
			return token, nil, nil
		})

	return func() *gitlab.CreateProjectDeployTokenOptions { return sent }
}

func TestCreate_Project(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectCreate(tc, createdToken())

	out, err := setupExec(t, tc)("ci-reader --scope read_repository")
	require.NoError(t, err)

	assert.Equal(t, "ci-reader", *sent().Name)
	assert.Equal(t, []string{"read_repository"}, *sent().Scopes)
	assert.Nil(t, sent().Username)
	assert.Nil(t, sent().ExpiresAt)

	expected := heredoc.Doc(`
		ID	42
		Name	ci-reader
		Username	gitlab+deploy-token-42
		Scopes	read_repository
		Expires At	-
		Token	gldt-secret
	`)
	assert.Equal(t, expected, out.OutBuf.String())
	assert.Equal(t, "! Created a deploy token for project OWNER/REPO. Store the token value now. It is not shown again.\n", out.ErrBuf.String())
}

func TestCreate_Group(t *testing.T) {
	t.Parallel()

	var sent *gitlab.CreateGroupDeployTokenOptions
	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		CreateGroupDeployToken("group/sub-group", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, opt *gitlab.CreateGroupDeployTokenOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DeployToken, *gitlab.Response, error) {
			sent = opt
			return &gitlab.DeployToken{
				ID:       7,
				Name:     "group-reader",
				Username: "gitlab+deploy-token-7",
				Scopes:   []string{"read_registry"},
				Token:    "gldt-group-secret",
			}, nil, nil
		})

	out, err := setupExec(t, tc)("group-reader --scope read_registry --group group/sub-group")
	require.NoError(t, err)

	assert.Equal(t, "group-reader", *sent.Name)
	assert.Equal(t, []string{"read_registry"}, *sent.Scopes)

	expected := heredoc.Doc(`
		ID	7
		Name	group-reader
		Username	gitlab+deploy-token-7
		Scopes	read_registry
		Expires At	-
		Token	gldt-group-secret
	`)
	assert.Equal(t, expected, out.OutBuf.String())
	assert.Equal(t, "! Created a deploy token for group group/sub-group. Store the token value now. It is not shown again.\n", out.ErrBuf.String())
}

// Unlike list and get, create is the one command whose output carries the token
// value, because this is the only time GitLab returns it.
func TestCreate_JSONKeepsTokenValue(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	expectProjectCreate(tc, createdToken())

	out, err := setupExec(t, tc)("ci-reader --scope read_repository --output json")
	require.NoError(t, err)

	var result gitlab.DeployToken
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	assert.Equal(t, int64(42), result.ID)
	assert.Equal(t, "ci-reader", result.Name)
	assert.Equal(t, "gitlab+deploy-token-42", result.Username)
	assert.Equal(t, "gldt-secret", result.Token)
	assert.Empty(t, out.ErrBuf.String())
}

func TestCreate_Scopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     string
		expected []string
	}{
		{name: "single", args: "--scope read_repository", expected: []string{"read_repository"}},
		{name: "repeated flag", args: "--scope read_repository --scope read_registry", expected: []string{"read_repository", "read_registry"}},
		{name: "comma-delimited", args: "--scope read_repository,read_registry", expected: []string{"read_repository", "read_registry"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			sent := expectProjectCreate(tc, createdToken())

			_, err := setupExec(t, tc)("ci-reader " + tt.args)
			require.NoError(t, err)

			assert.Equal(t, tt.expected, *sent().Scopes)
		})
	}
}

func TestCreate_Username(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectCreate(tc, createdToken())

	_, err := setupExec(t, tc)("ci-reader --scope read_repository --username deployer")
	require.NoError(t, err)

	assert.Equal(t, "deployer", *sent().Username)
}

// GitLab treats a blank username as absent and assigns the generated default,
// so this guards the Changed-based detection rather than a server rejection:
// a value test would drop the field instead of sending it.
func TestCreate_UsernameExplicitlyEmpty(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	sent := expectProjectCreate(tc, createdToken())

	_, err := setupExec(t, tc)("ci-reader --scope read_repository --username=")
	require.NoError(t, err)

	require.NotNil(t, sent().Username)
	assert.Empty(t, *sent().Username)
}

func TestCreate_ExpiresAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    string
		expected time.Time
	}{
		{name: "date only", value: "2027-12-31", expected: time.Date(2027, 12, 31, 0, 0, 0, 0, time.UTC)},
		{name: "RFC 3339", value: "2027-12-31T10:30:00Z", expected: time.Date(2027, 12, 31, 10, 30, 0, 0, time.UTC)},
		// 0001-01-01 parses to the zero time, which a value test would mistake
		// for an absent flag and send as "never expires".
		{name: "zero time", value: "0001-01-01", expected: time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			sent := expectProjectCreate(tc, createdToken())

			_, err := setupExec(t, tc)("ci-reader --scope read_repository --expires-at " + tt.value)
			require.NoError(t, err)

			require.NotNil(t, sent().ExpiresAt)
			assert.Equal(t, tt.expected, sent().ExpiresAt.UTC())
		})
	}
}

func TestCreate_InvalidExpiresAt(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("ci-reader --scope read_repository --expires-at tomorrow")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid argument")
}

func TestCreate_ScopeIsRequired(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("ci-reader")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `required flag(s) "scope" not set`)
}

func TestCreate_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		CreateProjectDeployToken("OWNER/REPO", gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("400 Bad Request"))

	_, err := setupExec(t, tc)("ci-reader --scope not_a_scope")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to create deploy token", exitErr.Details)
	assert.Contains(t, err.Error(), "400 Bad Request")
}

func TestCreate_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("ci-reader --scope read_repository --repo OWNER/REPO --group group/sub-group")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

// The project scope needs a base repository, and says so before any client is built.
func TestCreate_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("ci-reader --scope read_repository")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

// The group scope does not need a base repository.
func TestCreate_OutsideRepositoryWithGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		CreateGroupDeployToken("group/sub-group", gomock.Any(), gomock.Any()).
		Return(&gitlab.DeployToken{
			ID:       7,
			Name:     "group-reader",
			Username: "gitlab+deploy-token-7",
			Scopes:   []string{"read_repository"},
			Token:    "gldt-group-secret",
		}, nil, nil)

	out, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("group-reader --scope read_repository --group group/sub-group")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "group-reader")
}
