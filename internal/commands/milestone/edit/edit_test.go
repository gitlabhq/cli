package edit

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

func Test_EditProjectMilestone(t *testing.T) {
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
		Title:       "Example",
		Description: "Updated description",
		State:       "active",
		DueDate:     new(gitlab.ISOTime(time.Date(2025, 12, 16, 0, 0, 0, 0, time.UTC))),
	}

	testCases := []testCase{
		{
			Name:        "Edit project milestone",
			ExpectedMsg: []string{"Updated project milestone Example (ID: 123)"},
			cli:         "123 --title='Example' --project=456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().UpdateMilestone("456", int64(123), &gitlab.UpdateMilestoneOptions{
					Title: new("Example"),
				}).Return(testMilestone, nil, nil)
			},
		},
		{
			Name:        "Edit project milestone with description",
			ExpectedMsg: []string{"Updated project milestone Example (ID: 123)"},
			cli:         "123 --description='Updated description' --project 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().UpdateMilestone("456", int64(123), &gitlab.UpdateMilestoneOptions{
					Description: new("Updated description"),
				}).Return(testMilestone, nil, nil)
			},
		},
		{
			Name:       "When milestone is not found returns an error",
			wantErr:    true,
			wantStderr: "404 Not found",
			cli:        "111 --project 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().UpdateMilestone("456", int64(111), gomock.Any()).Return(nil, nil, errors.New("404 Not found"))
			},
		},
		{
			Name:       "When milestone is not set returns an error that it is required",
			wantErr:    true,
			wantStderr: "accepts 1 arg(s), received 0",
			cli:        "--project 456 --title='example'",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			Name: "When neither project nor group is set it updates the current project",
			cli:  "123 --title='Example'",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().UpdateMilestone("OWNER/REPO", int64(123), &gitlab.UpdateMilestoneOptions{
					Title: new("Example"),
				}).Return(testMilestone, nil, nil)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdEdit,
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

func Test_EditGroupMilestone(t *testing.T) {
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
		Title:       "Example",
		Description: "Updated description",
		State:       "active",
		DueDate:     new(gitlab.ISOTime(time.Date(2025, 12, 16, 0, 0, 0, 0, time.UTC))),
	}

	testCases := []testCase{
		{
			Name:        "Edit group milestone",
			ExpectedMsg: []string{"Updated group milestone Example (ID: 123)"},
			cli:         "123 --title='Example' --group=456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().UpdateGroupMilestone("456", int64(123), &gitlab.UpdateGroupMilestoneOptions{
					Title: new("Example"),
				}).Return(testMilestone, nil, nil)
			},
		},
		{
			Name:        "Edit group milestone with description",
			ExpectedMsg: []string{"Updated group milestone Example (ID: 123)"},
			cli:         "123 --description='Updated description' --group 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().UpdateGroupMilestone("456", int64(123), &gitlab.UpdateGroupMilestoneOptions{
					Description: new("Updated description"),
				}).Return(testMilestone, nil, nil)
			},
		},
		{
			Name:       "When milestone is not found returns an error",
			wantErr:    true,
			wantStderr: "404 Not found",
			cli:        "111 --group 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().UpdateGroupMilestone("456", int64(111), gomock.Any()).Return(nil, nil, errors.New("404 Not found"))
			},
		},
		{
			Name:       "When milestone is not set returns an error that it is required",
			wantErr:    true,
			wantStderr: "accepts 1 arg(s), received 0",
			cli:        "--group 456 --title='example'",
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
				NewCmdEdit,
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
func Test_EditProjectMilestone_BaseRepoError(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdEdit,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
		cmdtest.WithBaseRepoError(errors.New("no base repository")),
	)

	_, err := exec("123 --title Example")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no base repository")
}
