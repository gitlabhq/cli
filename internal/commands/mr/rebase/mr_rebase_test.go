//go:build !integration

package rebase

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMrRebase(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testMR := &gitlab.MergeRequest{
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
			name:        "when an MR is rebased using an MR id",
			cli:         "123",
			expectedOut: "✓ Rebase successful!\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(testMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					RebaseMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(nil, nil)
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							ID:  123,
							IID: 123,
						},
						RebaseInProgress: false,
						MergeError:       "",
					}, nil, nil)
			},
		},
		{
			name:        "when an MR is rebased with skip-ci flag",
			cli:         "123 --skip-ci",
			expectedOut: "✓ Rebase successful!\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(testMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					RebaseMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(nil, nil)
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							ID:  123,
							IID: 123,
						},
						RebaseInProgress: false,
						MergeError:       "",
					}, nil, nil)
			},
		},
		{
			name:        "when an MR is rebased using current branch",
			cli:         "",
			expectedOut: "✓ Rebase successful!\n",
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
					Return(testMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					RebaseMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(nil, nil)
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							ID:  123,
							IID: 123,
						},
						RebaseInProgress: false,
						MergeError:       "",
					}, nil, nil)
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
				NewCmdRebase,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBranch("current-branch"),
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
