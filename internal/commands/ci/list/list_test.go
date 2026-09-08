//go:build !integration

package list

import (
	"regexp"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestCiList(t *testing.T) {
	t.Parallel()

	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)

	createdAt1, _ := time.Parse(time.RFC3339, "2020-12-01T01:15:50.559Z")
	updatedAt1, _ := time.Parse(time.RFC3339, "2020-12-01T01:36:41.737Z")
	createdAt2, _ := time.Parse(time.RFC3339, "2020-11-30T18:20:47.571Z")
	updatedAt2, _ := time.Parse(time.RFC3339, "2020-11-30T18:39:40.092Z")

	testClient.MockPipelines.EXPECT().
		ListProjectPipelines("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.PipelineInfo{
			{
				ID:        1,
				IID:       3,
				ProjectID: 5,
				SHA:       "c366255c71600e17519e802850ddcf7105d3cf66",
				Ref:       "refs/merge-requests/1107/merge",
				Status:    "success",
				Source:    "merge_request_event",
				CreatedAt: &createdAt1,
				UpdatedAt: &updatedAt1,
				WebURL:    "https://gitlab.com/OWNER/REPO/-/pipelines/710046436",
			},
			{
				ID:        2,
				IID:       4,
				ProjectID: 5,
				SHA:       "c9a7c0d9351cd1e71d1c2ad8277f3bc7e3c47d1f",
				Ref:       "main",
				Status:    "success",
				Source:    "push",
				CreatedAt: &createdAt2,
				UpdatedAt: &updatedAt2,
				WebURL:    "https://gitlab.com/OWNER/REPO/-/pipelines/709793838",
			},
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdList,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	// WHEN
	output, err := exec("")

	// THEN
	require.NoError(t, err)

	out := output.String()
	timeRE := regexp.MustCompile(`\d+ years`)
	out = timeRE.ReplaceAllString(out, "X years")

	assert.Equal(t, heredoc.Doc(`
		Showing 2 pipelines on OWNER/REPO. (Page 1)

		State	IID	Ref	Created
		(success) • #1	(#3)	refs/merge-requests/1107/merge	(about X years ago)
		(success) • #2	(#4)	main	(about X years ago)

		`), out)
	assert.Empty(t, output.Stderr())
}

func TestCiListJSON(t *testing.T) {
	t.Parallel()

	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)

	createdAt1, _ := time.Parse(time.RFC3339, "2024-02-11T18:55:08.793Z")
	updatedAt1, _ := time.Parse(time.RFC3339, "2024-02-11T18:56:07.777Z")
	createdAt2, _ := time.Parse(time.RFC3339, "2024-02-10T18:55:16.722Z")
	updatedAt2, _ := time.Parse(time.RFC3339, "2024-02-10T18:56:13.972Z")

	testClient.MockPipelines.EXPECT().
		ListProjectPipelines("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.PipelineInfo{
			{
				ID:        1172622998,
				IID:       338,
				ProjectID: 37777023,
				Status:    "success",
				Source:    "schedule",
				Name:      "foo",
				Ref:       "#test#",
				SHA:       "3c890c11d784329052aa4ff63526dde2fa65b320",
				WebURL:    "https://gitlab.com/jay_mccure/test2target/-/pipelines/1172622998",
				UpdatedAt: &updatedAt1,
				CreatedAt: &createdAt1,
			},
			{
				ID:        1172086480,
				IID:       337,
				ProjectID: 37777023,
				Status:    "success",
				Source:    "schedule",
				Name:      "bar",
				Ref:       "#test#",
				SHA:       "3c890c11d784329052aa4ff63526dde2fa65b320",
				WebURL:    "https://gitlab.com/jay_mccure/test2target/-/pipelines/1172086480",
				UpdatedAt: &updatedAt2,
				CreatedAt: &createdAt2,
			},
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdList,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	// WHEN
	output, err := exec("-F json")

	// THEN
	require.NoError(t, err)

	expectedOut := `[
  {
    "id": 1172622998,
    "iid": 338,
    "project_id": 37777023,
    "status": "success",
    "source": "schedule",
    "name": "foo",
    "ref": "#test#",
    "sha": "3c890c11d784329052aa4ff63526dde2fa65b320",
    "web_url": "https://gitlab.com/jay_mccure/test2target/-/pipelines/1172622998",
    "updated_at": "2024-02-11T18:56:07.777Z",
    "created_at": "2024-02-11T18:55:08.793Z"
  },
  {
    "id": 1172086480,
    "iid": 337,
    "project_id": 37777023,
    "status": "success",
    "source": "schedule",
    "name": "bar",
    "ref": "#test#",
    "sha": "3c890c11d784329052aa4ff63526dde2fa65b320",
    "web_url": "https://gitlab.com/jay_mccure/test2target/-/pipelines/1172086480",
    "updated_at": "2024-02-10T18:56:13.972Z",
    "created_at": "2024-02-10T18:55:16.722Z"
  }
]`

	assert.JSONEq(t, expectedOut, output.String())
	assert.Empty(t, output.Stderr())
}
