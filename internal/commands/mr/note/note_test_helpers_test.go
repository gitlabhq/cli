//go:build !integration

package note

import (
	"testing"

	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"
)

// mockMR1 sets up a GetMergeRequest mock for MR !1 in OWNER/REPO.
// It is shared across all test files in the note package.
func mockMR1(t *testing.T, tc *gitlabtesting.TestClient) {
	t.Helper()
	mockMR1InRepo(t, tc, "OWNER/REPO", nil)
}

const mr1AuthorID int64 = 42

// mockMR1WithReviewers is mockMR1 with the MR authored by mr1AuthorID and the given reviewers.
func mockMR1WithReviewers(t *testing.T, tc *gitlabtesting.TestClient, reviewerIDs ...int64) {
	t.Helper()
	reviewers := make([]*gitlab.BasicUser, 0, len(reviewerIDs))
	for _, id := range reviewerIDs {
		reviewers = append(reviewers, &gitlab.BasicUser{ID: id})
	}
	mockMR1InRepo(t, tc, "OWNER/REPO", &gitlab.BasicUser{ID: mr1AuthorID}, reviewers...)
}

func mockMR1InRepo(t *testing.T, tc *gitlabtesting.TestClient, fullName string, author *gitlab.BasicUser, reviewers ...*gitlab.BasicUser) {
	t.Helper()
	tc.MockMergeRequests.EXPECT().
		GetMergeRequest(fullName, int64(1), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:        1,
				IID:       1,
				WebURL:    "https://gitlab.com/" + fullName + "/merge_requests/1",
				Author:    author,
				Reviewers: reviewers,
			},
		}, nil, nil)
}
