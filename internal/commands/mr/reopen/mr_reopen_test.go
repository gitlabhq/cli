//go:build !integration

package reopen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMrReopen(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testMRClosed := &gitlab.MergeRequest{
		BasicMergeRequest: gitlab.BasicMergeRequest{
			ID:          123,
			IID:         123,
			ProjectID:   3,
			Title:       "test mr title",
			Description: "test mr description",
			State:       "closed",
		},
	}

	testMROpened := &gitlab.MergeRequest{
		BasicMergeRequest: gitlab.BasicMergeRequest{
			ID:          123,
			IID:         123,
			ProjectID:   3,
			Title:       "test mr title",
			Description: "test mr description",
			State:       "opened",
		},
	}

	testCases := []testCase{
		{
			name:        "when an MR is reopened using an MR id",
			cli:         "123",
			expectedOut: "- Reopening merge request !123...\n✓ Reopened merge request !123.\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(testMRClosed, nil, nil)
				tc.MockMergeRequests.EXPECT().
					UpdateMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(testMROpened, nil, nil)
			},
		},
		{
			name:        "when an MR is reopened using a branch name",
			cli:         "foo",
			expectedOut: "- Reopening merge request !123...\n✓ Reopened merge request !123.\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.BasicMergeRequest{{
						ID:          123,
						IID:         123,
						ProjectID:   3,
						Title:       "test mr title",
						Description: "test mr description",
						State:       "opened",
					}}, nil, nil)
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(testMRClosed, nil, nil)
				tc.MockMergeRequests.EXPECT().
					UpdateMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(testMROpened, nil, nil)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdReopen,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}
