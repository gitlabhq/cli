//go:build !integration

package list

import (
	"net/http"
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

	assert.Equal(t, "Showing iteration 1 of 1 on OWNER/REPO.\n\n Iteration II -> Ipsum Lorem ipsum (http://gitlab.example.com/groups/my-group/-/iterations/13)\n \n", output.String())
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

	assert.Equal(t, "Showing iteration 1 of 1 for group my-group.\n\n Group Iteration -> Group iteration description (http://gitlab.example.com/groups/my-group/-/iterations/13)\n \n", output.String())
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

	assert.Equal(t, "Showing iteration 0 of 0 on OWNER/REPO.\n\n\n", output.String())
	assert.Empty(t, output.Stderr())
}
