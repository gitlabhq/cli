package list

import (
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

func Test_ListProjectMilestones(t *testing.T) {
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
			Name:        "List project milestones",
			ExpectedMsg: []string{"Title\tDescription\tState\tDue Date\nMilestone title\tExample description\tclosed\t2025-01-15\n\n"},
			cli:         "--project 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().ListMilestones("456", gomock.Any()).Return([]*gitlab.Milestone{testMilestone}, nil, nil)
			},
		},
		{
			Name:        "When --show-id is used shows a list of milestones with IDs",
			ExpectedMsg: []string{"ID\tTitle\tDescription\tState\tDue Date\n123\tMilestone title\tExample description\tclosed\t2025-01-15\n\n"},
			cli:         "--project 456 --show-id",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().ListMilestones("456", gomock.Any()).Return([]*gitlab.Milestone{testMilestone}, nil, nil)
			},
		},
		{
			Name:        "When no milestones are found returns a message",
			ExpectedMsg: []string{"No milestones found.\n"},
			cli:         "--project 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMilestones.EXPECT().ListMilestones("456", gomock.Any()).Return([]*gitlab.Milestone{}, nil, nil)
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
				NewCmdList,
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

func Test_ListGroupMilestones(t *testing.T) {
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
			Name:        "List group milestones",
			ExpectedMsg: []string{"Title\tDescription\tState\tDue Date\nMilestone title\tExample description\tclosed\t2025-01-15\n\n"},
			cli:         "--group 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().ListGroupMilestones("456", gomock.Any()).Return([]*gitlab.GroupMilestone{testMilestone}, nil, nil)
			},
		},
		{
			Name:        "When --show-id is used shows a list of milestones with IDs",
			ExpectedMsg: []string{"ID\tTitle\tDescription\tState\tDue Date\n123\tMilestone title\tExample description\tclosed\t2025-01-15\n\n"},
			cli:         "--group 456 --show-id",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().ListGroupMilestones("456", gomock.Any()).Return([]*gitlab.GroupMilestone{testMilestone}, nil, nil)
			},
		},
		{
			Name:        "When no milestones are found returns a message",
			ExpectedMsg: []string{"No milestones found.\n"},
			cli:         "--group 456",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().ListGroupMilestones("456", gomock.Any()).Return([]*gitlab.GroupMilestone{}, nil, nil)
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
				NewCmdList,
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

func TestMilestoneList_JSON(t *testing.T) {
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
		ListMilestones("456", gomock.Any()).
		Return([]*gitlab.Milestone{testMilestone}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdList,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	out, err := exec("--project 456 --output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"id":123`)
	assert.Contains(t, out.String(), `"title":"Milestone title"`)
	assert.Contains(t, out.String(), `"state":"closed"`)
	assert.Empty(t, out.Stderr())
}
