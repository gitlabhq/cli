//go:build !integration

package contributors

import (
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_ProjectContributors(t *testing.T) {
	type testCase struct {
		name           string
		cli            string
		expectedOutput string
		wantErr        bool
		wantStderr     string
		setupMock      func(tc *gitlabtesting.TestClient)
	}

	testContributors := []*gitlab.Contributor{
		{
			Name:    "Test User",
			Email:   "tu@gitlab.com",
			Commits: 41,
		},
		{
			Name:    "Test User2",
			Email:   "tu2@gitlab.com",
			Commits: 12,
		},
	}

	testCases := []testCase{
		{
			name: "view project contributors",
			cli:  "",
			expectedOutput: heredoc.Doc(`Showing 2 contributors on OWNER/REPO. (Page 1)

			Test User	tu@gitlab.com	41 commits
			Test User2	tu2@gitlab.com	12 commits

			`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockRepositories.EXPECT().
					Contributors("OWNER/REPO", gomock.Any()).
					Return(testContributors, nil, nil)
			},
		},
		{
			name: "view project contributors for a different project",
			cli:  "-R foo/bar",
			expectedOutput: heredoc.Doc(`Showing 2 contributors on foo/bar. (Page 1)

			Test User	tu@gitlab.com	41 commits
			Test User2	tu2@gitlab.com	12 commits

			`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockRepositories.EXPECT().
					Contributors("foo/bar", gomock.Any()).
					Return(testContributors, nil, nil)
			},
		},
		{
			name: "view project contributors ordered by name sorted in ascending order",
			cli:  "--order name --sort asc",
			expectedOutput: heredoc.Doc(`Showing 2 contributors on OWNER/REPO. (Page 1)

			Test User	tu@gitlab.com	41 commits
			Test User2	tu2@gitlab.com	12 commits

			`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockRepositories.EXPECT().
					Contributors("OWNER/REPO", gomock.Any()).
					Return(testContributors, nil, nil)
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
				NewCmdContributors,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
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
			assert.Equal(t, tc.expectedOutput, out.String())
			assert.Empty(t, out.Stderr())
		})
	}
}

func TestRepoContributors_JSON(t *testing.T) {
	t.Parallel()

	testContributors := []*gitlab.Contributor{
		{
			Name:    "Test User",
			Email:   "tu@gitlab.com",
			Commits: 41,
		},
		{
			Name:    "Test User2",
			Email:   "tu2@gitlab.com",
			Commits: 12,
		},
	}

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockRepositories.EXPECT().
		Contributors("OWNER/REPO", gomock.Any()).
		Return(testContributors, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdContributors,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("--output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"name":"Test User"`)
	assert.Contains(t, out.String(), `"email":"tu@gitlab.com"`)
	assert.Contains(t, out.String(), `"commits":41`)
	assert.Empty(t, out.Stderr())
}
