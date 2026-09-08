//go:build !integration

package add

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMembersAdd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		cli            string
		setupMocks     func(tc *gitlabtesting.TestClient)
		expectedOutput string
		expectedError  string
	}{
		{
			name: "add member by username with default role",
			cli:  "--username=john.doe",
			setupMocks: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					ListUsers(&gitlab.ListUsersOptions{Username: new("john.doe")}).
					Return([]*gitlab.User{{ID: 101, Username: "john.doe"}}, nil, nil)
				tc.MockProjectMembers.EXPECT().
					AddProjectMember("OWNER/REPO", &gitlab.AddProjectMemberOptions{
						UserID:      new(101),
						AccessLevel: new(gitlab.DeveloperPermissions),
					}).
					Return(&gitlab.ProjectMember{ID: 101, Username: "john.doe", AccessLevel: gitlab.DeveloperPermissions}, nil, nil)
			},
			expectedOutput: "✓ Successfully added john.doe as developer to OWNER/REPO\n",
		},
		{
			name: "add member by username with maintainer role",
			cli:  "--username=jane.smith --role=maintainer",
			setupMocks: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					ListUsers(&gitlab.ListUsersOptions{Username: new("jane.smith")}).
					Return([]*gitlab.User{{ID: 102, Username: "jane.smith"}}, nil, nil)
				tc.MockProjectMembers.EXPECT().
					AddProjectMember("OWNER/REPO", &gitlab.AddProjectMemberOptions{
						UserID:      new(102),
						AccessLevel: new(gitlab.MaintainerPermissions),
					}).
					Return(&gitlab.ProjectMember{ID: 102, Username: "jane.smith", AccessLevel: gitlab.MaintainerPermissions}, nil, nil)
			},
			expectedOutput: "✓ Successfully added jane.smith as maintainer to OWNER/REPO\n",
		},
		{
			name: "add member by user ID",
			cli:  "--user-id=123 --role=reporter",
			setupMocks: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectMembers.EXPECT().
					AddProjectMember("OWNER/REPO", &gitlab.AddProjectMemberOptions{
						UserID:      new(123),
						AccessLevel: new(gitlab.ReporterPermissions),
					}).
					Return(&gitlab.ProjectMember{ID: 123, Username: "testuser", AccessLevel: gitlab.ReporterPermissions}, nil, nil)
			},
			expectedOutput: "✓ Successfully added testuser as reporter to OWNER/REPO\n",
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
			name:          "error when both role and role-id provided",
			cli:           "--username=john.doe --role=developer --role-id=101",
			setupMocks:    func(tc *gitlabtesting.TestClient) {},
			expectedError: "were all set",
		},
		{
			name:          "error with invalid role",
			cli:           "--username=john.doe --role=invalid",
			setupMocks:    func(tc *gitlabtesting.TestClient) {},
			expectedError: "invalid role: invalid. Valid roles are: guest, reporter, developer, maintainer, owner",
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
				if assert.NoErrorf(t, err, "error running command `members add %s`: %v", tc.cli, err) {
					assert.Equal(t, tc.expectedOutput, out.OutBuf.String())
					assert.Empty(t, out.Stderr())
				}
			}
		})
	}
}
