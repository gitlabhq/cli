//go:build !integration

package remove

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMembersRemove(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		cli            string
		setupMocks     func(tc *gitlabtesting.TestClient)
		expectedOutput string
		expectedError  string
	}{
		{
			name: "remove member by username",
			cli:  "--username=john.doe",
			setupMocks: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					ListUsers(&gitlab.ListUsersOptions{Username: new("john.doe")}).
					Return([]*gitlab.User{{ID: 1, Username: "john.doe"}}, nil, nil)
				tc.MockProjectMembers.EXPECT().
					DeleteProjectMember("OWNER/REPO", int64(1)).
					Return(nil, nil)
			},
			expectedOutput: "✓ Successfully removed john.doe from OWNER/REPO\n",
		},
		{
			name: "remove member by user ID",
			cli:  "--user-id=123",
			setupMocks: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectMembers.EXPECT().
					DeleteProjectMember("OWNER/REPO", int64(123)).
					Return(nil, nil)
			},
			expectedOutput: "✓ Successfully removed 123 from OWNER/REPO\n",
		},
		{
			name:          "error when no username or user-id provided",
			cli:           "",
			setupMocks:    func(tc *gitlabtesting.TestClient) {},
			expectedError: "either username or user-id must be specified",
		},
		{
			name:          "error when both username and user-id provided",
			cli:           "--username=john.doe --user-id=123",
			setupMocks:    func(tc *gitlabtesting.TestClient) {},
			expectedError: "were all set",
		},
		{
			name: "error when user not found",
			cli:  "--username=nonexistent",
			setupMocks: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					ListUsers(&gitlab.ListUsersOptions{Username: new("nonexistent")}).
					Return([]*gitlab.User{}, nil, nil)
			},
			expectedError: "user nonexistent not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testClient := gitlabtesting.NewTestClient(t)
			if tc.setupMocks != nil {
				tc.setupMocks(testClient)
			}

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
				cmdtest.WithGitLabClient(testClient.Client),
			)

			out, err := exec(tc.cli)

			if tc.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				if assert.NoErrorf(t, err, "error running command `members remove %s`: %v", tc.cli, err) {
					assert.Equal(t, tc.expectedOutput, out.OutBuf.String())
					assert.Empty(t, out.Stderr())
				}
			}
		})
	}
}
