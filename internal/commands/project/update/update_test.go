//go:build !integration

package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_ProjectUpdate(t *testing.T) {
	type testCase struct {
		name      string
		cli       string
		wantErr   bool
		errString string
		setupMock func(tc *gitlabtesting.TestClient)
	}

	projectResult := &gitlab.Project{
		NameWithNamespace: "user / repo",
		WebURL:            "https://gitlab.com/user/repo",
	}

	testUserResult := &gitlab.Project{
		NameWithNamespace: "test_user / repo",
		WebURL:            "https://gitlab.com/test_user/repo",
	}

	otherProjectResult := &gitlab.Project{
		NameWithNamespace: "otheruser / myproject",
		WebURL:            "https://gitlab.com/otheruser/myproject",
	}

	urlProjectResult := &gitlab.Project{
		NameWithNamespace: "user / project",
		WebURL:            "https://gitlab.com/user/project",
	}

	testCases := []testCase{
		{
			name: "Update description for current repo",
			cli:  "--description foo",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					EditProject("OWNER/REPO", gomock.Any()).
					Return(projectResult, nil, nil)
			},
		},
		{
			name: "Update description for user's repo",
			cli:  "repo --description foo",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser().
					Return(&gitlab.User{Username: "test_user"}, nil, nil)
				tc.MockProjects.EXPECT().
					EditProject("test_user/repo", gomock.Any()).
					Return(testUserResult, nil, nil)
			},
		},
		{
			name: "Update description for other repo",
			cli:  "otheruser/myproject --description foo",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					EditProject("otheruser/myproject", gomock.Any()).
					Return(otherProjectResult, nil, nil)
			},
		},
		{
			name: "Update description for repo at URL",
			cli:  "https://gitlab.com/user/project --description foo",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					EditProject(gomock.Any(), gomock.Any()).
					Return(urlProjectResult, nil, nil)
			},
		},
		{
			name: "Update default branch",
			cli:  "--defaultBranch main2",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					EditProject("OWNER/REPO", gomock.Any()).
					Return(projectResult, nil, nil)
			},
		},
		{
			name: "Update both description and default branch at the same time",
			cli:  "--description foo --defaultBranch main2",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					EditProject("OWNER/REPO", gomock.Any()).
					Return(projectResult, nil, nil)
			},
		},
		{
			name:      "No flags provided",
			cli:       "",
			wantErr:   true,
			errString: "at least one of the flags in the group",
			setupMock: func(tc *gitlabtesting.TestClient) {},
		},
		{
			name: "Archive project with just --archive flag",
			cli:  "--archive",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ArchiveProject("OWNER/REPO").
					Return(projectResult, nil, nil)
			},
		},
		{
			name: "Archive project with --archive=true",
			cli:  "--archive=true",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ArchiveProject("OWNER/REPO").
					Return(projectResult, nil, nil)
			},
		},
		{
			name: "Unarchive project",
			cli:  "--archive=false",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					UnarchiveProject("OWNER/REPO").
					Return(projectResult, nil, nil)
			},
		},
		{
			name: "Archive project and change description at the same time",
			cli:  "--archive=true --description=foobar",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					EditProject("OWNER/REPO", gomock.Any()).
					Return(projectResult, nil, nil)
				tc.MockProjects.EXPECT().
					ArchiveProject("OWNER/REPO").
					Return(projectResult, nil, nil)
			},
		},
		{
			name: "Unarchive project and change default branch at the same time",
			cli:  "--archive=false --defaultBranch=main2",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					EditProject("OWNER/REPO", gomock.Any()).
					Return(projectResult, nil, nil)
				tc.MockProjects.EXPECT().
					UnarchiveProject("OWNER/REPO").
					Return(projectResult, nil, nil)
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
				NewCmdUpdate,
				true,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", glinstance.DefaultHostname, api.WithGitLabClient(testClient.Client))),
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			_, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errString)
				return
			}
			require.NoError(t, err)
		})
	}
}
