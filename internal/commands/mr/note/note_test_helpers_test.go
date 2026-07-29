//go:build !integration

package note

import (
	"testing"

	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"
)

// mockMR1 sets up a GetMergeRequest mock for MR !1 in OWNER/REPO.
// It is shared across all test files in the note package.
func mockMR1(t *testing.T, tc *gitlabtesting.TestClient) {
	t.Helper()
	mockMR1InRepo(t, tc, "OWNER/REPO")
}

func mockMR1InRepo(t *testing.T, tc *gitlabtesting.TestClient, fullName string) {
	t.Helper()
	tc.MockMergeRequests.EXPECT().
		GetMergeRequest(fullName, int64(1), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:     1,
				IID:    1,
				WebURL: "https://gitlab.com/" + fullName + "/merge_requests/1",
			},
		}, nil, nil)
}
