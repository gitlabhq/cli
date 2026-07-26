//go:build !integration

package todo

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMrTodo(t *testing.T) {
	type testCase struct {
		name          string
		cli           string
		expectedOut   string
		wantErr       bool
		expectedError error
		setupMock     func(tc *gitlabtesting.TestClient)
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
			name:        "when an MR is added as a todo using an MR id",
			cli:         "123",
			expectedOut: "✓ Done!!\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(testMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					CreateTodo("OWNER/REPO", int64(123)).
					Return(nil, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusCreated}}, nil)
			},
		},
		{
			name:        "when an MR is added as a todo using a branch name",
			cli:         "foo",
			expectedOut: "✓ Done!!\n",
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
					CreateTodo("OWNER/REPO", int64(123)).
					Return(nil, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusCreated}}, nil)
			},
		},
		{
			name:          "when todo already exists",
			cli:           "foo",
			wantErr:       true,
			expectedError: errTodoExists,
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
					CreateTodo("OWNER/REPO", int64(123)).
					Return(nil, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotModified}}, nil)
			},
		},
		{
			name:          "when the request fails without a response",
			cli:           "123",
			wantErr:       true,
			expectedError: errors.New("network unavailable"),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(testMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					CreateTodo("OWNER/REPO", int64(123)).
					Return(nil, nil, errors.New("network unavailable"))
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
				NewCmdTodo,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				if tc.expectedError != nil {
					assert.Equal(t, tc.expectedError, err)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}
