//go:build !integration

package get

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

func Test_GetDeployKey(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testKey := &gitlab.ProjectDeployKey{
		ID:        1,
		Title:     "example key",
		Key:       "ssh-ed25519 example",
		CanPush:   false,
		CreatedAt: new(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
	}

	emptyKey := &gitlab.ProjectDeployKey{}

	testCases := []testCase{
		{
			name:        "when no deploy key is found shows appropriate message",
			cli:         "1",
			expectedOut: "Deploy key does not exist.\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockDeployKeys.EXPECT().
					GetDeployKey("OWNER/REPO", int64(1), gomock.Any()).
					Return(emptyKey, nil, nil)
			},
		},
		{
			name:        "when a deploy key is found shows its details",
			cli:         "1",
			expectedOut: "Title\tKey\tCan Push\tCreated At\nexample key\tssh-ed25519 example\tfalse\t2025-01-01 00:00:00 +0000 UTC\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockDeployKeys.EXPECT().
					GetDeployKey("OWNER/REPO", int64(1), gomock.Any()).
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
				NewCmdGet,
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

func TestDeployKeyGet_JSON(t *testing.T) {
	t.Parallel()

	testKey := &gitlab.ProjectDeployKey{
		ID:        1,
		Title:     "example key",
		Key:       "ssh-ed25519 example",
		CanPush:   false,
		CreatedAt: new(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
	}

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockDeployKeys.EXPECT().
		GetDeployKey("OWNER/REPO", int64(1), gomock.Any()).
		Return(testKey, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdGet,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("1 --output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"id":1`)
	assert.Contains(t, out.String(), `"title":"example key"`)
	assert.Contains(t, out.String(), `"key":"ssh-ed25519 example"`)
	assert.Contains(t, out.String(), `"can_push":false`)
	assert.Empty(t, out.Stderr())
}
