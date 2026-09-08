//go:build !integration

package done

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestTodoDone(t *testing.T) {
	t.Parallel()
	type testCase struct {
		name          string
		cli           string
		expectedOut   string
		wantErr       bool
		expectedError string
		setupMock     func(tc *gitlabtesting.TestClient)
	}

	testCases := []testCase{
		{
			name:        "marks a single todo as done by ID",
			cli:         "42",
			expectedOut: "✓ To-do item 42 marked as done.\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockTodos.EXPECT().
					MarkTodoAsDone(int64(42)).
					Return(&gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil)
			},
		},
		{
			name:        "marks all todos as done with --all",
			cli:         "--all",
			expectedOut: "✓ All to-do items marked as done.\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockTodos.EXPECT().
					MarkAllTodosAsDone().
					Return(&gitlab.Response{Response: &http.Response{StatusCode: http.StatusNoContent}}, nil)
			},
		},
		{
			name:          "errors when no ID and no --all flag",
			cli:           "",
			wantErr:       true,
			expectedError: "either a to-do ID or --all is required",
			setupMock:     func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:          "errors when both ID and --all are provided",
			cli:           "42 --all",
			wantErr:       true,
			expectedError: "--all cannot be used with a to-do ID",
			setupMock:     func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:          "errors when ID is not a valid number",
			cli:           "not-a-number",
			wantErr:       true,
			expectedError: `invalid to-do ID: "not-a-number"`,
			setupMock:     func(tc *gitlabtesting.TestClient) {},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}
