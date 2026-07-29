//go:build !integration

package list

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_ProjectList(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testProject := &gitlab.Project{
		ID:                123,
		Description:       "This is a test project",
		PathWithNamespace: "gitlab-org/incubation-engineering/service-desk/meta",
	}

	testUserProject := &gitlab.Project{
		ID:                123,
		Description:       "This is a test project",
		PathWithNamespace: "testuser/example",
	}

	testGroup := &gitlab.Group{
		ID:       456,
		Path:     "subgroup",
		FullPath: "me/group/subgroup",
	}

	emptyProjectResponse := &gitlab.Response{
		TotalItems:  0,
		CurrentPage: 1,
		TotalPages:  1,
	}

	singleProjectResponse := &gitlab.Response{
		TotalItems:  1,
		CurrentPage: 1,
		TotalPages:  1,
	}

	testCases := []testCase{
		{
			name:        "when no projects are found shows an empty list",
			cli:         "",
			expectedOut: "Showing 0 of 0 projects (Page 1 of 1).\n\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{}, emptyProjectResponse, nil)
			},
		},
		{
			name:        "when no arguments, filters by ownership",
			cli:         "",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "when starred is passed as an arg, filters by starred",
			cli:         "--starred",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "when member is passed as an arg, filters by member",
			cli:         "--member",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "when mine is passed explicitly as an arg, filters by ownership",
			cli:         "--mine",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "when mine and starred are passed as args, filters by ownership and starred",
			cli:         "--mine --starred",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "when starred and member are passed as args, filters by starred and membership",
			cli:         "--starred --member",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "when mine and membership are passed as args, filters by ownership and membership",
			cli:         "--mine --member",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "when mine, membership and starred is passed explicitly as arguments, filters by ownership, membership and starred",
			cli:         "--mine --member --starred",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "view all projects, no filters",
			cli:         "--all",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "view all projects ordered by created_at date sorted descending",
			cli:         "--order created_at --sort desc",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "view all projects in a specific group",
			cli:         "--group me/group/subgroup",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroups.EXPECT().
					GetGroup("me/group/subgroup", gomock.Any()).
					Return(testGroup, nil, nil)
				tc.MockGroups.EXPECT().
					ListGroupProjects(int64(456), gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "view all projects in a specific group including subgroups",
			cli:         "--group me/group/subgroup --include-subgroups",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroups.EXPECT().
					GetGroup("me/group/subgroup", gomock.Any()).
					Return(testGroup, nil, nil)
				tc.MockGroups.EXPECT().
					ListGroupProjects(int64(456), gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "view all not archived projects in a specific group",
			cli:         "-a --group me/group/subgroup --archived=false",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroups.EXPECT().
					GetGroup("me/group/subgroup", gomock.Any()).
					Return(testGroup, nil, nil)
				tc.MockGroups.EXPECT().
					ListGroupProjects(int64(456), gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "view all archived projects in a specific group",
			cli:         "-a --group me/group/subgroup --archived=true",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroups.EXPECT().
					GetGroup("me/group/subgroup", gomock.Any()).
					Return(testGroup, nil, nil)
				tc.MockGroups.EXPECT().
					ListGroupProjects(int64(456), gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "view all archived projects",
			cli:         "-a --archived=true",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "view all not archived projects",
			cli:         "-a --archived=false",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ngitlab-org/incubation-engineering/service-desk/meta\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListProjects(gomock.Any()).
					Return([]*gitlab.Project{testProject}, singleProjectResponse, nil)
			},
		},
		{
			name:        "view all projects for a given user",
			cli:         "-u testuser",
			expectedOut: "Showing 1 of 1 projects (Page 1 of 1).\n\nProject path\tGit URL\tDescription\ntestuser/example\t\tThis is a test project\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					ListUserProjects("testuser", gomock.Any()).
					Return([]*gitlab.Project{testUserProject}, singleProjectResponse, nil)
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
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", glinstance.DefaultHostname, api.WithGitLabClient(testClient.Client))),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantStderr, err.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedOut, out.String())
			assert.Empty(t, out.Stderr())
		})
	}
}
