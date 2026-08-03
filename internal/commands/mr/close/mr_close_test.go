//go:build !integration

package close

import (
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMrClose(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testMROpened := &gitlab.MergeRequest{
		BasicMergeRequest: gitlab.BasicMergeRequest{
			ID:                  123,
			IID:                 123,
			ProjectID:           3,
			Title:               "test mr title",
			Description:         "test mr description",
			State:               "opened",
			DetailedMergeStatus: "ci_must_pass",
		},
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

	testCases := []testCase{
		{
			name: "when an MR is closed using an MR id",
			cli:  "123",
			expectedOut: heredoc.Doc(`
				- Closing merge request...
				✓ Closed merge request !123.

			`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(testMROpened, nil, nil)
				tc.MockMergeRequests.EXPECT().
					UpdateMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(testMRClosed, nil, nil)
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
				NewCmdClose,
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
