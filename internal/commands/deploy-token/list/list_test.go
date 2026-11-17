//go:build !integration

package list

import (
	"encoding/json"
	"errors"
	"net/http"
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

func noMorePages() *gitlab.Response {
	return &gitlab.Response{NextPage: 0}
}

func setupExec(t *testing.T, tc *gitlabtesting.TestClient) cmdtest.CmdExecFunc {
	t.Helper()

	return cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)
}

func TestList_Project(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		ListProjectDeployTokens("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.DeployToken{
			{
				ID:        1,
				Name:      "ci-reader",
				Username:  "gitlab+deploy-token-1",
				ExpiresAt: &expiresAt,
				Scopes:    []string{"read_repository", "read_registry"},
			},
			{
				ID:       2,
				Name:     "retired",
				Username: "gitlab+deploy-token-2",
				Revoked:  true,
				Scopes:   []string{"read_repository"},
			},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc)("")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	Name	Username	Scopes	Expires At	Revoked	Expired
		1	ci-reader	gitlab+deploy-token-1	read_repository, read_registry	2027-01-01T00:00:00Z	false	false
		2	retired	gitlab+deploy-token-2	read_repository	-	true	false
	`)
	assert.Equal(t, expected, out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestList_Group(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		ListGroupDeployTokens("group/sub-group", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.DeployToken{
			{
				ID:       7,
				Name:     "group-reader",
				Username: "gitlab+deploy-token-7",
				Scopes:   []string{"read_repository"},
			},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc)("--group group/sub-group")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	Name	Username	Scopes	Expires At	Revoked	Expired
		7	group-reader	gitlab+deploy-token-7	read_repository	-	false	false
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

// The deploy token list endpoints accept no active filter, so --active is
// applied to the tokens that were retrieved.
func TestList_ActiveOnly(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		ListProjectDeployTokens("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.DeployToken{
			{ID: 1, Name: "active", Username: "gitlab+deploy-token-1", Scopes: []string{"read_repository"}},
			{ID: 2, Name: "revoked", Username: "gitlab+deploy-token-2", Revoked: true, Scopes: []string{"read_repository"}},
			{ID: 3, Name: "expired", Username: "gitlab+deploy-token-3", Expired: true, Scopes: []string{"read_repository"}},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc)("--active")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	Name	Username	Scopes	Expires At	Revoked	Expired
		1	active	gitlab+deploy-token-1	read_repository	-	false	false
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

func TestList_JSON(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		ListProjectDeployTokens("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.DeployToken{
			{
				ID:        1,
				Name:      "ci-reader",
				Username:  "gitlab+deploy-token-1",
				ExpiresAt: &expiresAt,
				Scopes:    []string{"read_repository", "read_registry"},
			},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc)("--output json")
	require.NoError(t, err)

	var result []*gitlab.DeployToken
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, int64(1), result[0].ID)
	assert.Equal(t, "ci-reader", result[0].Name)
	assert.Equal(t, "gitlab+deploy-token-1", result[0].Username)
	assert.Equal(t, expiresAt, *result[0].ExpiresAt)
	assert.Equal(t, []string{"read_repository", "read_registry"}, result[0].Scopes)
	assert.False(t, result[0].Revoked)
	assert.False(t, result[0].Expired)
}

// A token value must never reach the output, in either format.
func TestList_OmitsTokenValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args string
	}{
		{name: "text", args: ""},
		{name: "json", args: "--output json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			tc.MockDeployTokens.EXPECT().
				ListProjectDeployTokens("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
				Return([]*gitlab.DeployToken{
					{ID: 1, Name: "ci-reader", Username: "gitlab+deploy-token-1", Token: "gldt-secret", Scopes: []string{"read_repository"}},
				}, noMorePages(), nil)

			out, err := setupExec(t, tc)(tt.args)
			require.NoError(t, err)

			assert.Contains(t, out.OutBuf.String(), "ci-reader")
			assert.NotContains(t, out.OutBuf.String(), "gldt-secret")
		})
	}
}

func TestList_Empty(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		ListProjectDeployTokens("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.DeployToken{}, noMorePages(), nil)

	out, err := setupExec(t, tc)("")
	require.NoError(t, err)

	assert.Equal(t, "No deploy tokens found.\n", out.OutBuf.String())
}

func TestList_EmptyJSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		ListProjectDeployTokens("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.DeployToken{}, noMorePages(), nil)

	out, err := setupExec(t, tc)("--output json")
	require.NoError(t, err)

	var result []*gitlab.DeployToken
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	assert.Empty(t, result)
}

func TestList_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		ListProjectDeployTokens("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("403 Forbidden"))

	_, err := setupExec(t, tc)("")
	require.Error(t, err)

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to list deploy tokens", exitErr.Details)
	assert.Contains(t, err.Error(), "403 Forbidden")
}

func TestList_RepoAndGroupAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	_, err := setupExec(t, tc)("--repo OWNER/REPO --group group/sub-group")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

// The project scope needs a base repository, and says so before any client is
// built, so the caller is told why the repository was needed.
func TestList_OutsideRepositoryWithoutGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepoError(errors.New("not a git repository")),
	)

	_, err := exec("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

// The group scope does not need a base repository.
func TestList_OutsideRepositoryWithGroup(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		ListGroupDeployTokens("group/sub-group", gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.DeployToken{
			{ID: 7, Name: "group-reader", Username: "gitlab+deploy-token-7", Scopes: []string{"read_repository"}},
		}, noMorePages(), nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepoError(errors.New("not a git repository")),
	)

	out, err := exec("--group group/sub-group")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	Name	Username	Scopes	Expires At	Revoked	Expired
		7	group-reader	gitlab+deploy-token-7	read_repository	-	false	false
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

func TestList_Instance(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		ListAllDeployTokens(gomock.Any(), gomock.Any()).
		Return([]*gitlab.DeployToken{
			{ID: 3, Name: "registry-pull", Username: "gitlab+deploy-token-3", Scopes: []string{"read_registry"}},
		}, noMorePages(), nil)

	out, err := setupExec(t, tc)("--instance")
	require.NoError(t, err)

	expected := heredoc.Doc(`
		ID	Name	Username	Scopes	Expires At	Revoked	Expired
		3	registry-pull	gitlab+deploy-token-3	read_registry	-	false	false
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

func TestList_InstanceOutsideRepository(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		ListAllDeployTokens(gomock.Any(), gomock.Any()).
		Return([]*gitlab.DeployToken{
			{ID: 3, Name: "registry-pull", Username: "gitlab+deploy-token-3", Scopes: []string{"read_registry"}},
		}, noMorePages(), nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepoError(errors.New("not a git repository")),
	)

	out, err := exec("--instance")
	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), "registry-pull")
}

// Any call to the group endpoint fails the mock, so this proves GITLAB_GROUP
// does not redirect --instance.
func TestList_InstanceIgnoresGroupEnv(t *testing.T) {
	t.Setenv("GITLAB_GROUP", "env-group")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockDeployTokens.EXPECT().
		ListAllDeployTokens(gomock.Any(), gomock.Any()).
		Return([]*gitlab.DeployToken{}, noMorePages(), nil)

	out, err := setupExec(t, tc)("--instance")
	require.NoError(t, err)
	assert.Equal(t, "No deploy tokens found.\n", out.OutBuf.String())
}

func TestList_InstanceAPIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		wantHint bool
	}{
		{name: "forbidden names the administrator requirement", status: http.StatusForbidden, wantHint: true},
		{name: "other failures are passed through", status: http.StatusInternalServerError, wantHint: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			tc.MockDeployTokens.EXPECT().
				ListAllDeployTokens(gomock.Any(), gomock.Any()).
				Return(nil, nil, &gitlab.ErrorResponse{StatusCode: tt.status, Message: http.StatusText(tt.status)})

			_, err := setupExec(t, tc)("--instance")
			require.Error(t, err)

			var exitErr *cmdutils.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, "failed to list deploy tokens", exitErr.Details)
			assert.True(t, gitlab.HasStatusCode(err, tt.status))
			if tt.wantHint {
				assert.Contains(t, err.Error(), "requires administrator access")
			} else {
				assert.NotContains(t, err.Error(), "administrator")
			}
		})
	}
}

func TestList_InstanceIsMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args string
	}{
		{name: "with repo", args: "--instance --repo OWNER/REPO"},
		{name: "with group", args: "--instance --group group/sub-group"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)

			_, err := setupExec(t, tc)(tt.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "none of the others can be")
		})
	}
}
