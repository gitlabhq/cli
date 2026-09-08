//go:build !integration

package list

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

func TestDeployKeyList(t *testing.T) {
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

	testCases := []testCase{
		{
			name:        "when no deploy keys are found shows an empty list",
			cli:         "",
			expectedOut: "\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockDeployKeys.EXPECT().
					ListProjectDeployKeys("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.ProjectDeployKey{}, nil, nil)
			},
		},
		{
			name:        "when deploy keys are found shows a list of keys",
			cli:         "",
			expectedOut: "Title\tKey\tCan Push\tCreated At\nexample key\tssh-ed25519 example\tfalse\t2025-01-01 00:00:00 +0000 UTC\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockDeployKeys.EXPECT().
					ListProjectDeployKeys("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.ProjectDeployKey{testKey}, nil, nil)
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
				NewCmdList,
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

func TestDeployKeyList_JSON(t *testing.T) {
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
		ListProjectDeployKeys("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.ProjectDeployKey{testKey}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdList,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("--output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"id":1`)
	assert.Contains(t, out.String(), `"title":"example key"`)
	assert.Contains(t, out.String(), `"key":"ssh-ed25519 example"`)
	assert.Contains(t, out.String(), `"can_push":false`)
	assert.Empty(t, out.Stderr())
}
