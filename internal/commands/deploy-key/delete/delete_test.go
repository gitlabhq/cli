//go:build !integration

package delete

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_DeployKeyRemove(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedMsg []string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testCases := []testCase{
		{
			name:        "Remove a deploy key",
			cli:         "123",
			expectedMsg: []string{"Deploy key deleted.\n"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockDeployKeys.EXPECT().
					DeleteDeployKey("OWNER/REPO", int64(123)).
					Return(nil, nil)
			},
		},
		{
			name:       "Remove a deploy key without a key ID",
			cli:        "",
			wantErr:    true,
			wantStderr: "accepts 1 arg(s), received 0",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:       "Remove a deploy key with invalid key ID",
			cli:        "abc",
			wantErr:    true,
			wantStderr: "deploy key ID must be an integer: abc",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:       "Remove non-existent deploy key returns error",
			cli:        "999",
			wantErr:    true,
			wantStderr: "404 Not Found",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockDeployKeys.EXPECT().
					DeleteDeployKey("OWNER/REPO", int64(999)).
					Return(nil, errors.New("404 Not Found"))
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
				NewCmdDelete,
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
			for _, msg := range tc.expectedMsg {
				assert.Contains(t, out.OutBuf.String(), msg)
			}
		})
	}
}
