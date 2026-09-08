//go:build !integration

package issues

import (
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMergeRequestClosesIssues_byID(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	createdAt, _ := time.Parse(time.RFC3339, "2020-09-05T01:17:17.270Z")

	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:     123,
				IID:    123,
				WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/123",
			},
		}, nil, nil)

	testClient.MockMergeRequests.EXPECT().
		GetIssuesClosedOnMerge("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return([]*gitlab.Issue{
			{
				ID:        123,
				IID:       11,
				ProjectID: 1,
				Title:     "new issue",
				State:     "opened",
				CreatedAt: &createdAt,
				Labels:    gitlab.Labels{},
			},
			{
				ID:        123,
				IID:       15,
				ProjectID: 1,
				Title:     "this is another new issue",
				State:     "opened",
				CreatedAt: &createdAt,
				Labels:    gitlab.Labels{},
			},
		}, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdIssues,
		true,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	// WHEN
	output, err := exec("123")

	// THEN
	require.NoError(t, err)

	out := output.String()
	timeRE := regexp.MustCompile(`\d+ years`)
	out = timeRE.ReplaceAllString(out, "X years")

	assert.Contains(t, out, "Showing 2 issues in OWNER/REPO that match your search.")
	assert.Contains(t, out, "#11\tnew issue")
	assert.Contains(t, out, "#15\tthis is another new issue")
	assert.Contains(t, out, "about X years ago")
	assert.Empty(t, output.Stderr())
}

func TestMergeRequestClosesIssues_currentBranch(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	createdAt, _ := time.Parse(time.RFC3339, "2020-09-05T01:17:17.270Z")

	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any()).
		Return([]*gitlab.BasicMergeRequest{
			{
				ID:        123,
				IID:       123,
				ProjectID: 1,
				WebURL:    "https://gitlab.com/OWNER/REPO/merge_requests/123",
			},
		}, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil)

	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:     123,
				IID:    123,
				WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/123",
			},
		}, nil, nil)

	testClient.MockMergeRequests.EXPECT().
		GetIssuesClosedOnMerge("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return([]*gitlab.Issue{
			{
				ID:        123,
				IID:       11,
				ProjectID: 1,
				Title:     "new issue",
				State:     "opened",
				CreatedAt: &createdAt,
				Labels:    gitlab.Labels{},
			},
			{
				ID:        123,
				IID:       15,
				ProjectID: 1,
				Title:     "this is another new issue",
				State:     "opened",
				CreatedAt: &createdAt,
				Labels:    gitlab.Labels{},
			},
		}, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdIssues,
		true,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBranch("current_branch"),
	)

	// WHEN
	output, err := exec("")

	// THEN
	require.NoError(t, err)

	out := output.String()
	timeRE := regexp.MustCompile(`\d+ years`)
	out = timeRE.ReplaceAllString(out, "X years")

	assert.Contains(t, out, "Showing 2 issues in OWNER/REPO that match your search.")
	assert.Contains(t, out, "#11\tnew issue")
	assert.Contains(t, out, "#15\tthis is another new issue")
	assert.Contains(t, out, "about X years ago")
	assert.Empty(t, output.Stderr())
}
