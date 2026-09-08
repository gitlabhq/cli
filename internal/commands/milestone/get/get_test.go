package get

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

func Test_GetProjectMilestone(t *testing.T) {
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
		Title:       "Milestone title",
		Description: "Example description",
		State:       "closed",
		DueDate:     new(gitlab.ISOTime(time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC))),
	}

	testCases := []testCase{
		{
			Name:        "Get project milestone",
			ExpectedMsg: []string{"Title: Milestone title\nDescription: Example description\nState: closed\nDue Date: 2025-01-15\n\n"},
			cli:         "123 --project 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().GetMilestone("456", int64(123)).Return(testMilestone, nil, nil)
			},
		},
		{
			Name:    "When milestone is not found returns an error",
			wantErr: true,
			cli:     "111 --project 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().GetMilestone("456", int64(111)).Return(nil, nil, errors.New("404 Not found"))
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
				NewCmdGet,
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

func Test_GetGroupMilestone(t *testing.T) {
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
		Title:       "Milestone title",
		Description: "Example description",
		State:       "closed",
		DueDate:     new(gitlab.ISOTime(time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC))),
	}

	testCases := []testCase{
		{
			Name:        "Get group milestone",
			ExpectedMsg: []string{"Title: Milestone title\nDescription: Example description\nState: closed\nDue Date: 2025-01-15\n\n"},
			cli:         "123 --group 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().GetGroupMilestone(gomock.Any(), int64(123)).Return(testMilestone, nil, nil)
			},
		},
		{
			Name:    "When milestone is not found returns an error",
			wantErr: true,
			cli:     "111 --group 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().GetGroupMilestone(gomock.Any(), int64(111)).Return(nil, nil, errors.New("404 Not found"))
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
				NewCmdGet,
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

func TestMilestoneGet_JSON(t *testing.T) {
	t.Parallel()

	testMilestone := &gitlab.Milestone{
		ID:          123,
		ProjectID:   456,
		Title:       "Milestone title",
		Description: "Example description",
		State:       "closed",
		DueDate:     new(gitlab.ISOTime(time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC))),
	}

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockMilestones.EXPECT().
		GetMilestone("456", int64(123)).
		Return(testMilestone, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdGet,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	out, err := exec("123 --project 456 --output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"id":123`)
	assert.Contains(t, out.String(), `"title":"Milestone title"`)
	assert.Contains(t, out.String(), `"state":"closed"`)
	assert.Empty(t, out.Stderr())
}

// Without an explicit --project or --group, these commands fall back to the
// base repository. When that lookup fails the error has to surface instead of
// being dropped, otherwise the nil repository is dereferenced and glab panics.
func Test_GetProjectMilestone_BaseRepoError(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdGet,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
		cmdtest.WithBaseRepoError(errors.New("no base repository")),
	)

	_, err := exec("123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no base repository")
}
