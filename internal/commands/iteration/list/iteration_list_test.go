//go:build !integration

package list

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestIterationList(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjectIterations.EXPECT().
		ListProjectIterations("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.ProjectIteration{
			{
				ID:          53,
				IID:         13,
				GroupID:     5,
				Title:       "Iteration II",
				Description: "Ipsum Lorem ipsum",
				State:       2,
				WebURL:      "http://gitlab.example.com/groups/my-group/-/iterations/13",
			},
		}, nil, nil)

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmdList, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("")
	require.NoError(t, err)

	assert.Equal(t, "Showing 1 iteration on OWNER/REPO. (Page 1)\n\n Iteration II -> Ipsum Lorem ipsum (http://gitlab.example.com/groups/my-group/-/iterations/13)\n \n", output.String())
	assert.Empty(t, output.Stderr())
}

func TestIterationListJSON(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjectIterations.EXPECT().
		ListProjectIterations("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.ProjectIteration{
			{
				ID:          53,
				IID:         13,
				GroupID:     5,
				Title:       "Iteration II",
				Description: "Ipsum Lorem ipsum",
				State:       2,
				WebURL:      "https://gitlab.com/api/v4/projects/OWNER%2FREPO/iterations?include_ancestors=true&page=1&per_page=30",
			},
		}, nil, nil)

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmdList, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("-F json")
	require.NoError(t, err)

	expectedBody := `[
  {
    "id": 53,
    "iid": 13,
    "group_id": 5,
    "title": "Iteration II",
    "description": "Ipsum Lorem ipsum",
    "state": 2,
    "created_at": null,
    "updated_at": null,
    "due_date": null,
    "start_date": null,
    "sequence": 0,
    "web_url": "https://gitlab.com/api/v4/projects/OWNER%2FREPO/iterations?include_ancestors=true&page=1&per_page=30"
  }
]`

	assert.JSONEq(t, expectedBody, output.String())
	assert.Empty(t, output.Stderr())
}

func TestIterationListGroup(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockGroupIterations.EXPECT().
		ListGroupIterations("my-group", gomock.Any()).
		Return([]*gitlab.GroupIteration{
			{
				ID:          53,
				IID:         13,
				Title:       "Group Iteration",
				Description: "Group iteration description",
				State:       1,
				WebURL:      "http://gitlab.example.com/groups/my-group/-/iterations/13",
			},
		}, nil, nil)

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmdList, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("-g my-group")
	require.NoError(t, err)

	assert.Equal(t, "Showing 1 iteration on my-group. (Page 1)\n\n Group Iteration -> Group iteration description (http://gitlab.example.com/groups/my-group/-/iterations/13)\n \n", output.String())
	assert.Empty(t, output.Stderr())
}

func TestIterationListEmpty(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjectIterations.EXPECT().
		ListProjectIterations("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.ProjectIteration{}, nil, nil)

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmdList, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("")
	require.NoError(t, err)

	assert.Equal(t, "No iterations available on OWNER/REPO.\n\n", output.String())
	assert.Empty(t, output.Stderr())
}

func TestIterationListGroupEmpty(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockGroupIterations.EXPECT().
		ListGroupIterations("my-group", gomock.Any()).
		Return([]*gitlab.GroupIteration{}, nil, nil)

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmdList, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("-g my-group")
	require.NoError(t, err)

	assert.Equal(t, "No iterations available on my-group.\n\n", output.String())
	assert.Empty(t, output.Stderr())
}

func TestIterationListHeaderTotal(t *testing.T) {
	t.Parallel()

	projectIterations := []*gitlab.ProjectIteration{
		{Title: "Iteration II", WebURL: "http://gitlab.example.com/groups/my-group/-/iterations/13"},
	}
	groupIterations := []*gitlab.GroupIteration{
		{Title: "Group Iteration", WebURL: "http://gitlab.example.com/groups/my-group/-/iterations/13"},
	}

	tests := []struct {
		name       string
		cli        string
		setupMock  func(tc *gitlabtesting.TestClient)
		wantHeader string
	}{
		{
			name: "project shows the total from the API",
			cli:  "",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectIterations.EXPECT().
					ListProjectIterations("OWNER/REPO", gomock.Any()).
					Return(projectIterations, &gitlab.Response{CurrentPage: 1, TotalPages: 25, TotalItems: 25}, nil)
			},
			wantHeader: "Showing 1 of 25 iterations on OWNER/REPO. (Page 1)\n",
		},
		{
			name: "group shows the total from the API",
			cli:  "-g my-group --per-page 1 --page 25",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupIterations.EXPECT().
					ListGroupIterations("my-group", gomock.Any()).
					Return(groupIterations, &gitlab.Response{CurrentPage: 25, TotalPages: 25, TotalItems: 25}, nil)
			},
			wantHeader: "Showing 1 of 25 iterations on my-group. (Page 25)\n",
		},
		{
			name: "project without a total from the API",
			cli:  "",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectIterations.EXPECT().
					ListProjectIterations("OWNER/REPO", gomock.Any()).
					Return(projectIterations, &gitlab.Response{CurrentPage: 1, NextPage: 2}, nil)
			},
			wantHeader: "Showing 1 iteration on OWNER/REPO. (Page 1)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t)
			tt.setupMock(testClient)

			apiClient, err := api.NewClient(
				func(*http.Client) (gitlab.AuthSource, error) {
					return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
				},
				api.WithGitLabClient(testClient.Client),
			)
			require.NoError(t, err)

			exec := cmdtest.SetupCmdForTest(t, NewCmdList, true,
				cmdtest.WithApiClient(apiClient),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			output, err := exec(tt.cli)
			require.NoError(t, err)

			assert.True(t, strings.HasPrefix(output.String(), tt.wantHeader), "got %q", output.String())
			assert.Empty(t, output.Stderr())
		})
	}
}
