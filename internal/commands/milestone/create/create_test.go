package create

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_CreateProjectMilestone(t *testing.T) {
	type testCase struct {
		Name        string
		ExpectedMsg []string
		wantErr     bool
		cli         string
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testMilestone := &gitlab.Milestone{
		ID:          123,
		ProjectID:   456,
		Title:       "Example title",
		Description: "Example description",
		State:       "active",
		DueDate:     new(gitlab.ISOTime(time.Date(2025, 12, 16, 0, 0, 0, 0, time.UTC))),
	}

	testCases := []testCase{
		{
			Name:        "Create project milestone",
			ExpectedMsg: []string{"Created project milestone Example title (ID: 123)"},
			cli:         "--title='Example title' --project=456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().CreateMilestone("456", gomock.Any()).Return(testMilestone, nil, nil)
			},
		},
		{
			Name:        "Create project milestone with specific due date",
			ExpectedMsg: []string{"Created project milestone Example title (ID: 123)"},
			cli:         "--title='Example title' --due-date='2025-12-16' --project 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().CreateMilestone("456", gomock.Any()).Return(testMilestone, nil, nil)
			},
		},
		{
			Name:       "Should return an error if title is not supplied",
			wantErr:    true,
			wantStderr: "required flag(s) \"title\" not set",
			cli:        "--due-date='2025-12-16' --project 456",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdCreate,
				false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
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
			for _, msg := range tc.ExpectedMsg {
				assert.Equal(t, msg, out.OutBuf.String())
			}
		})
	}
}

func Test_CreateGroupMilestone(t *testing.T) {
	type testCase struct {
		Name        string
		ExpectedMsg []string
		wantErr     bool
		cli         string
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testMilestone := &gitlab.GroupMilestone{
		ID:          123,
		GroupID:     456,
		Title:       "Example title",
		Description: "Example description",
		State:       "active",
		DueDate:     new(gitlab.ISOTime(time.Date(2025, 12, 16, 0, 0, 0, 0, time.UTC))),
	}

	testCases := []testCase{
		{
			Name:        "Create group milestone",
			ExpectedMsg: []string{"Created group milestone Example title (ID: 123)"},
			cli:         "--title='Example title' --group=456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().CreateGroupMilestone("456", gomock.Any()).Return(testMilestone, nil, nil)
			},
		},
		{
			Name:        "Create group milestone with specific due date",
			ExpectedMsg: []string{"Created group milestone Example title (ID: 123)"},
			cli:         "--title='Example title' --due-date='2025-12-16' --group 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().CreateGroupMilestone("456", gomock.Any()).Return(testMilestone, nil, nil)
			},
		},
		{
			Name:       "Should return an error if title is not supplied",
			wantErr:    true,
			wantStderr: "required flag(s) \"title\" not set",
			cli:        "--due-date='2025-12-16' --group 456",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdCreate,
				false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
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
			for _, msg := range tc.ExpectedMsg {
				assert.Equal(t, msg, out.OutBuf.String())
			}
		})
	}
}

// Without an explicit --project or --group, these commands fall back to the
// base repository. When that lookup fails the error has to surface instead of
// being dropped, otherwise the nil repository is dereferenced and glab panics.
func Test_CreateProjectMilestone_BaseRepoError(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdCreate,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
		cmdtest.WithBaseRepoError(errors.New("no base repository")),
	)

	_, err := exec("--title Example")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no base repository")
}
