package delete

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_DeleteProjectMilestone(t *testing.T) {
	type testCase struct {
		Name        string
		ExpectedMsg []string
		wantErr     bool
		cli         string
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testCases := []testCase{
		{
			Name:        "Delete project milestone",
			ExpectedMsg: []string{"Deleted project milestone with ID 123.\n"},
			cli:         "123 --project 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().DeleteMilestone("456", int64(123)).Return(nil, nil)
			},
		},
		{
			Name:       "When milestone is not found returns an error",
			wantErr:    true,
			wantStderr: "404 Not found",
			cli:        "111 --project 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().DeleteMilestone("456", int64(111)).Return(nil, errors.New("404 Not found"))
			},
		},
		{
			Name:       "When milestone ID is not set returns an error that it is required",
			wantErr:    true,
			wantStderr: "accepts 1 arg(s), received 0",
			cli:        "--project 456",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			Name: "When neither project nor group is set it deletes the milestone from the current project",
			cli:  "123",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().DeleteMilestone("OWNER/REPO", int64(123), gomock.Any()).Return(nil, nil)
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
				NewCmdDelete,
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

func Test_DeleteGroupMilestone(t *testing.T) {
	type testCase struct {
		Name        string
		ExpectedMsg []string
		wantErr     bool
		cli         string
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testCases := []testCase{
		{
			Name:        "Delete group milestone",
			ExpectedMsg: []string{"Deleted group milestone with ID 123.\n"},
			cli:         "123 --group 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().DeleteGroupMilestone("456", int64(123)).Return(nil, nil)
			},
		},
		{
			Name:       "When milestone is not found returns an error",
			wantErr:    true,
			wantStderr: "404 Not found",
			cli:        "111 --group 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().DeleteGroupMilestone("456", int64(111)).Return(nil, errors.New("404 Not found"))
			},
		},
		{
			Name:       "When milestone ID is not set returns an error that it is required",
			wantErr:    true,
			wantStderr: "accepts 1 arg(s), received 0",
			cli:        "--group 456",
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
				NewCmdDelete,
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
func Test_DeleteProjectMilestone_BaseRepoError(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdDelete,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
		cmdtest.WithBaseRepoError(errors.New("no base repository")),
	)

	_, err := exec("123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no base repository")
}
