//go:build !integration

package serviceaccountutils

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func noMorePages() *gitlab.Response {
	return &gitlab.Response{NextPage: 0}
}

func TestNewClient(t *testing.T) {
	t.Parallel()

	repo := func() (glrepo.Interface, error) {
		return glrepo.NewWithHost("OWNER", "REPO", "gitlab.example.com"), nil
	}
	noRepo := func() (glrepo.Interface, error) {
		return nil, errors.New("not a git repository")
	}

	t.Run("project scope uses the base repository and its host", func(t *testing.T) {
		t.Parallel()

		var gotHost string
		apiClient := func(host string) (*api.Client, error) {
			gotHost = host
			return cmdtest.NewTestApiClient(t, nil, "", ""), nil
		}

		_, scope, err := NewClient(repo, apiClient, "")
		require.NoError(t, err)

		assert.Equal(t, Scope{Project: "OWNER/REPO"}, scope)
		assert.Equal(t, "project OWNER/REPO", scope.String())
		assert.Equal(t, "gitlab.example.com", gotHost)
	})

	t.Run("group scope works outside a repository", func(t *testing.T) {
		t.Parallel()

		var gotHost string
		apiClient := func(host string) (*api.Client, error) {
			gotHost = host
			return cmdtest.NewTestApiClient(t, nil, "", ""), nil
		}

		_, scope, err := NewClient(noRepo, apiClient, "my-group")
		require.NoError(t, err)

		assert.Equal(t, Scope{Group: "my-group"}, scope)
		assert.Equal(t, "group my-group", scope.String())
		assert.Empty(t, gotHost)
	})

	t.Run("project scope outside a repository fails before building a client", func(t *testing.T) {
		t.Parallel()

		apiClient := func(string) (*api.Client, error) {
			t.Fatal("the API client must not be built")
			return nil, nil
		}

		_, _, err := NewClient(noRepo, apiClient, "")
		require.EqualError(t, err, "not a git repository")
	})
}

func TestParseID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ref     string
		wantID  int64
		wantOK  bool
		wantErr string
	}{
		{ref: "42", wantID: 42, wantOK: true},
		{ref: "service_account_group_1_aaa"},
		{ref: "0", wantErr: `invalid service account ID "0"`},
		{ref: "-1", wantErr: `invalid service account ID "-1"`},
		{ref: "99999999999999999999", wantErr: `invalid service account ID "99999999999999999999"`},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			t.Parallel()

			id, ok, err := ParseID(tt.ref)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, id)
			assert.Equal(t, tt.wantOK, ok)
		})
	}
}

func TestFindByUsername(t *testing.T) {
	t.Parallel()

	groupAccounts := []*gitlab.GroupServiceAccount{
		{ID: 41, UserName: "service_account_group_1_aaa", Name: "Deployer", PublicEmail: "public@example.com"},
		{ID: 42, UserName: "service_account_group_1_bbb", Name: "Releaser"},
	}
	projectAccounts := []*gitlab.ProjectServiceAccount{
		{ID: 51, Username: "service_account_project_2_aaa", Name: "Deployer"},
		{ID: 52, Username: "service_account_project_2_bbb", Name: "Releaser"},
	}

	t.Run("returns the listed group account", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockGroups.EXPECT().
			ListServiceAccounts("my-group", gomock.Any(), gomock.Any(), gomock.Any()).
			Return(groupAccounts, noMorePages(), nil)

		raw, account, err := FindByUsername(t.Context(), tc.Client, Scope{Group: "my-group"}, "service_account_group_1_bbb")
		require.NoError(t, err)
		assert.Same(t, groupAccounts[1], raw)
		assert.Equal(t, int64(42), account.ID)
	})

	t.Run("returns the listed project account", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
			Return(projectAccounts, noMorePages(), nil)

		raw, account, err := FindByUsername(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, "service_account_project_2_bbb")
		require.NoError(t, err)
		assert.Same(t, projectAccounts[1], raw)
		assert.Equal(t, int64(52), account.ID)
	})

	t.Run("a group lookup requests 100 accounts per page", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockGroups.EXPECT().
			ListServiceAccounts("my-group", gomock.Cond(func(o *gitlab.ListServiceAccountsOptions) bool { return o.PerPage == 100 }), gomock.Any(), gomock.Any()).
			Return(groupAccounts, noMorePages(), nil)

		_, _, err := FindByUsername(t.Context(), tc.Client, Scope{Group: "my-group"}, "service_account_group_1_bbb")
		require.NoError(t, err)
	})

	t.Run("a project lookup requests 100 accounts per page", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			ListProjectServiceAccounts("OWNER/REPO", gomock.Cond(func(o *gitlab.ListProjectServiceAccountsOptions) bool { return o.PerPage == 100 }), gomock.Any(), gomock.Any()).
			Return(projectAccounts, noMorePages(), nil)

		_, _, err := FindByUsername(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, "service_account_project_2_bbb")
		require.NoError(t, err)
	})

	// The first page announces a next page, so fetching it fails the Times(1)
	// expectation.
	t.Run("the lookup stops at the first page with a match", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockGroups.EXPECT().
			ListServiceAccounts("my-group", gomock.Any(), gomock.Any(), gomock.Any()).
			Return(groupAccounts, &gitlab.Response{NextPage: 2}, nil).
			Times(1)

		_, account, err := FindByUsername(t.Context(), tc.Client, Scope{Group: "my-group"}, "service_account_group_1_aaa")
		require.NoError(t, err)
		assert.Equal(t, int64(41), account.ID)
	})

	t.Run("a username matches regardless of case", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockGroups.EXPECT().
			ListServiceAccounts("my-group", gomock.Any(), gomock.Any(), gomock.Any()).
			Return(groupAccounts, noMorePages(), nil)

		_, account, err := FindByUsername(t.Context(), tc.Client, Scope{Group: "my-group"}, "SERVICE_ACCOUNT_GROUP_1_BBB")
		require.NoError(t, err)
		assert.Equal(t, int64(42), account.ID)
	})

	t.Run("a username that is not found returns no account", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockGroups.EXPECT().
			ListServiceAccounts("my-group", gomock.Any(), gomock.Any(), gomock.Any()).
			Return(groupAccounts, noMorePages(), nil)

		raw, _, err := FindByUsername(t.Context(), tc.Client, Scope{Group: "my-group"}, "missing")
		require.EqualError(t, err, `no service account with username "missing" in group my-group`)
		assert.Nil(t, raw)
	})

	t.Run("a list error is returned", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, nil, errors.New("403 Forbidden"))

		_, _, err := FindByUsername(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, "service_account_project_2_bbb")
		require.EqualError(t, err, "403 Forbidden")
	})
}

func TestResolveID(t *testing.T) {
	t.Parallel()

	// No expectation is set, so any API call fails the test.
	t.Run("numeric ID makes no request", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)

		id, err := ResolveID(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, "52")
		require.NoError(t, err)
		assert.Equal(t, int64(52), id)
	})

	t.Run("username found in a group", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockGroups.EXPECT().
			ListServiceAccounts("my-group", gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*gitlab.GroupServiceAccount{
				{ID: 41, UserName: "service_account_group_1_aaa"},
				{ID: 42, UserName: "service_account_group_1_bbb"},
			}, noMorePages(), nil)

		id, err := ResolveID(t.Context(), tc.Client, Scope{Group: "my-group"}, "service_account_group_1_bbb")
		require.NoError(t, err)
		assert.Equal(t, int64(42), id)
	})

	t.Run("username found in a project", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*gitlab.ProjectServiceAccount{
				{ID: 51, Username: "service_account_project_2_aaa"},
			}, noMorePages(), nil)

		id, err := ResolveID(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, "service_account_project_2_aaa")
		require.NoError(t, err)
		assert.Equal(t, int64(51), id)
	})

	t.Run("username not found", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			ListProjectServiceAccounts("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*gitlab.ProjectServiceAccount{}, noMorePages(), nil)

		id, err := ResolveID(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, "missing")
		require.EqualError(t, err, `no service account with username "missing" in project OWNER/REPO`)
		assert.Zero(t, id)
	})

	t.Run("invalid ID", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)

		_, err := ResolveID(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, "0")
		require.EqualError(t, err, `invalid service account ID "0"`)
	})
}

func TestPrintDetails(t *testing.T) {
	t.Parallel()

	t.Run("without a pending email", func(t *testing.T) {
		t.Parallel()

		io, _, stdout, _ := cmdtest.TestIOStreams()
		PrintDetails(io, Account{ID: 41, Username: "service_account_group_1_aaa"})

		assert.Equal(t, "ID\t41\nUsername\tservice_account_group_1_aaa\nName\t-\nEmail\t-\n", stdout.String())
	})

	t.Run("with a pending email", func(t *testing.T) {
		t.Parallel()

		io, _, stdout, _ := cmdtest.TestIOStreams()
		PrintDetails(io, Account{
			ID:               41,
			Username:         "release-bot",
			Name:             "Release bot",
			Email:            "service_account_group_1_aaa@noreply.gitlab.example.com",
			UnconfirmedEmail: "release-bot@example.com",
		})

		assert.Equal(t, "ID\t41\nUsername\trelease-bot\nName\tRelease bot\nEmail\tservice_account_group_1_aaa@noreply.gitlab.example.com\nUnconfirmed Email\trelease-bot@example.com\n", stdout.String())
	})
}

func TestFromGroupAndFromProject(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		Account{ID: 41, Username: "release-bot", Name: "Release bot", Email: "a@example.com", UnconfirmedEmail: "b@example.com"},
		FromGroup(&gitlab.GroupServiceAccount{ID: 41, UserName: "release-bot", Name: "Release bot", Email: "a@example.com", UnconfirmedEmail: "b@example.com"}),
	)
	assert.Equal(t,
		Account{ID: 51, Username: "release-bot", Name: "Release bot", Email: "a@example.com", UnconfirmedEmail: "b@example.com"},
		FromProject(&gitlab.ProjectServiceAccount{ID: 51, Username: "release-bot", Name: "Release bot", Email: "a@example.com", UnconfirmedEmail: "b@example.com"}),
	)
}

func TestOrDash(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "-", OrDash(""))
	assert.Equal(t, "Deployer", OrDash("Deployer"))
}
