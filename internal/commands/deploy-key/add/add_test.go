//go:build !integration

package add

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_AddDeployKey(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedMsg []string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testKey := &gitlab.ProjectDeployKey{
		ID:        12,
		Title:     "My deploy key",
		Key:       "ssh-rsa AAAA...",
		CanPush:   true,
		CreatedAt: new(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
	}

	testCases := []testCase{
		{
			name:        "Add deploy key",
			cli:         "testdata/testkey.key -t testkey",
			expectedMsg: []string{"New deploy key added."},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockDeployKeys.EXPECT().
					AddDeployKey("OWNER/REPO", gomock.Any()).
					Return(testKey, nil, nil)
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
				NewCmdAdd,
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
