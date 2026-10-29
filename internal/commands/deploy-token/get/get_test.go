//go:build !integration

package get

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

func TestGet_Project(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		GetProjectDeployToken("OWNER/REPO", int64(42), gomock.Any()).
		Return(&gitlab.DeployToken{
			ID:        42,
			Name:      "ci-reader",
			Username:  "gitlab+deploy-token-42",
			ExpiresAt: &expiresAt,
			Scopes:    []string{"read_repository", "read_registry"},
		}, nil, nil)

	out, err := setupExec(t, tc)("42")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	42
		Name	ci-reader
		Username	gitlab+deploy-token-42
		Scopes	read_repository, read_registry
		Expires At	2027-01-01T00:00:00Z
		Revoked	false
		Expired	false
	`)
	assert.Equal(t, expected, out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestGet_Group(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		GetGroupDeployToken("group/sub-group", int64(7), gomock.Any()).
		Return(&gitlab.DeployToken{
			ID:       7,
			Name:     "group-reader",
			Username: "gitlab+deploy-token-7",
			Scopes:   []string{"read_repository"},
		}, nil, nil)

	out, err := setupExec(t, tc)("7 --group group/sub-group")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	7
		Name	group-reader
		Username	gitlab+deploy-token-7
		Scopes	read_repository
		Expires At	-
		Revoked	false
		Expired	false
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

func TestGet_JSON(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		GetProjectDeployToken("OWNER/REPO", int64(42), gomock.Any()).
		Return(&gitlab.DeployToken{
			ID:        42,
			Name:      "ci-reader",
			Username:  "gitlab+deploy-token-42",
			ExpiresAt: &expiresAt,
			Revoked:   true,
			Expired:   true,
			Scopes:    []string{"read_repository"},
		}, nil, nil)

	out, err := setupExec(t, tc)("42 --output json")
	require.NoError(t, err)

	var result gitlab.DeployToken
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	assert.Equal(t, int64(42), result.ID)
	assert.Equal(t, "ci-reader", result.Name)
	assert.Equal(t, "gitlab+deploy-token-42", result.Username)
	assert.Equal(t, expiresAt, *result.ExpiresAt)
	assert.Equal(t, []string{"read_repository"}, result.Scopes)
	assert.True(t, result.Revoked)
	assert.True(t, result.Expired)
}

// A token value must never reach the output, in either format.
func TestGet_OmitsTokenValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args string
	}{
		{name: "text", args: "42"},
		{name: "json", args: "42 --output json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			tc.MockDeployTokens.EXPECT().
				GetProjectDeployToken("OWNER/REPO", int64(42), gomock.Any()).
				Return(&gitlab.DeployToken{
					ID:       42,
					Name:     "ci-reader",
					Username: "gitlab+deploy-token-42",
					Scopes:   []string{"read_repository"},
					Token:    "gldt-secret",
				}, nil, nil)

			out, err := setupExec(t, tc)(tt.args)
			require.NoError(t, err)

			assert.Contains(t, out.OutBuf.String(), "ci-reader")
			assert.NotContains(t, out.OutBuf.String(), "gldt-secret")
		})
	}
}

func TestGet_InvalidID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("not-a-number")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "invalid deploy token ID", exitErr.Details)
	// ExitError renders only the wrapped error, so that is what the user sees.
	// The rejected value must not lead the message, because the error handler
	// capitalizes the first character and would rewrite the value.
	assert.Equal(t, `invalid deploy token ID: "not-a-number"`, err.Error())
}

func TestGet_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		GetProjectDeployToken("OWNER/REPO", int64(42), gomock.Any()).
		Return(nil, nil, errors.New("404 Not Found"))

	_, err := setupExec(t, tc)("42")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to get deploy token", exitErr.Details)
	assert.Contains(t, err.Error(), "404 Not Found")
}

func TestGet_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("42 --repo OWNER/REPO --group group/sub-group")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

// The project scope needs a base repository, and says so before any client is built.
func TestGet_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("42")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

// The group scope does not need a base repository.
func TestGet_OutsideRepositoryWithGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		GetGroupDeployToken("group/sub-group", int64(7), gomock.Any()).
		Return(&gitlab.DeployToken{
			ID:       7,
			Name:     "group-reader",
			Username: "gitlab+deploy-token-7",
			Scopes:   []string{"read_repository"},
		}, nil, nil)

	out, err := setupExec(t, tc, cmdtest.WithBaseRepoError(errors.New("not a git repository")))("7 --group group/sub-group")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "group-reader")
}
