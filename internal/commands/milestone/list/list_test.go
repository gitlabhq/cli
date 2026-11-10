package list

import (
	"encoding/json"
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
		{
			Name:       "When --include-descendants is used with --project returns error",
			cli:        "--project 456 --include-descendants",
			wantErr:    true,
			wantStderr: "if any flags in the group [project include-descendants] are set none of the others can be",
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
		Name          string
		ExpectedMsg   []string
		wantErr       bool
		cli           string
		wantStderr    string
		clientOptions []gitlab.ClientOptionFunc
		setupMock     func(tc *gitlabtesting.TestClient)
	}

	testMilestone := &gitlab.GroupMilestone{
		ID:          123,
		GroupID:     456,
		Title:       "Milestone title",
		Description: "Example description",
		State:       "closed",
		DueDate:     new(gitlab.ISOTime(time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC))),
	}

	groupMilestone := &gitlab.GroupMilestone{
		ID:          123,
		GroupID:     456,
		Title:       "Group milestone",
		Description: "Group description",
		State:       "active",
		DueDate:     new(gitlab.ISOTime(time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC))),
		WebURL:      "https://gitlab.com/groups/example-group/-/milestones/123",
	}

	projectMilestone := &gitlab.GroupMilestone{
		ID:          124,
		ProjectID:   789,
		Title:       "Project milestone",
		Description: "Project description",
		State:       "active",
		DueDate:     new(gitlab.ISOTime(time.Date(2025, 2, 20, 0, 0, 0, 0, time.UTC))),
		WebURL:      "https://gitlab.com/example-group/example-project/-/milestones/124",
	}

	fallbackMilestone := &gitlab.GroupMilestone{
		ID:          125,
		ProjectID:   790,
		Title:       "Fallback project milestone",
		Description: "Fallback description",
		State:       "active",
		DueDate:     new(gitlab.ISOTime(time.Date(2025, 3, 25, 0, 0, 0, 0, time.UTC))),
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
		{
			Name: "When --include-descendants is used lists group and descendant project milestones",
			ExpectedMsg: []string{
				"Title\tOwner\tDescription\tState\tDue Date\n" +
					"Group milestone\texample-group\tGroup description\tactive\t2025-01-15\n" +
					"Project milestone\texample-group/example-project\tProject description\tactive\t2025-02-20\n\n",
			},
			cli: "--group 456 --include-descendants",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().ListGroupMilestones("456", &gitlab.ListGroupMilestonesOptions{
					ListOptions: gitlab.ListOptions{
						Page:    1,
						PerPage: 20,
					},
					IncludeDescendents: new(true),
				}).Return([]*gitlab.GroupMilestone{groupMilestone, projectMilestone}, nil, nil)
			},
		},
		{
			Name: "When --include-descendants and --show-id are used",
			ExpectedMsg: []string{
				"ID\tTitle\tOwner\tDescription\tState\tDue Date\n" +
					"123\tGroup milestone\texample-group\tGroup description\tactive\t2025-01-15\n" +
					"124\tProject milestone\texample-group/example-project\tProject description\tactive\t2025-02-20\n\n",
			},
			cli: "--group 456 --include-descendants --show-id",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().ListGroupMilestones("456", &gitlab.ListGroupMilestonesOptions{
					ListOptions: gitlab.ListOptions{
						Page:    1,
						PerPage: 20,
					},
					IncludeDescendents: new(true),
				}).Return([]*gitlab.GroupMilestone{groupMilestone, projectMilestone}, nil, nil)
			},
		},
		{
			Name: "When --include-descendants is used and milestones have no WebURL",
			ExpectedMsg: []string{
				"Title\tOwner\tDescription\tState\tDue Date\n" +
					"Fallback project milestone\tproject/790\tFallback description\tactive\t2025-03-25\n\n",
			},
			cli: "--group 456 --include-descendants",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().ListGroupMilestones("456", &gitlab.ListGroupMilestonesOptions{
					ListOptions: gitlab.ListOptions{
						Page:    1,
						PerPage: 20,
					},
					IncludeDescendents: new(true),
				}).Return([]*gitlab.GroupMilestone{fallbackMilestone}, nil, nil)
			},
		},
		{
			Name: "When --include-descendants is used on sub-path instance",
			ExpectedMsg: []string{
				"Title\tOwner\tDescription\tState\tDue Date\n" +
					"Group milestone\texample-group\tGroup description\tactive\t2025-01-15\n" +
					"Project milestone\texample-group/example-project\tProject description\tactive\t2025-02-20\n\n",
			},
			cli:           "--group 456 --include-descendants",
			clientOptions: []gitlab.ClientOptionFunc{gitlab.WithBaseURL("https://example.com/gitlab/api/v4/")},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupMilestones.EXPECT().ListGroupMilestones("456", &gitlab.ListGroupMilestonesOptions{
					ListOptions: gitlab.ListOptions{
						Page:    1,
						PerPage: 20,
					},
					IncludeDescendents: new(true),
				}).Return([]*gitlab.GroupMilestone{
					{
						ID:          123,
						GroupID:     456,
						Title:       "Group milestone",
						Description: "Group description",
						State:       "active",
						DueDate:     new(gitlab.ISOTime(time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC))),
						WebURL:      "https://example.com/gitlab/groups/example-group/-/milestones/123",
					},
					{
						ID:          124,
						ProjectID:   789,
						Title:       "Project milestone",
						Description: "Project description",
						State:       "active",
						DueDate:     new(gitlab.ISOTime(time.Date(2025, 2, 20, 0, 0, 0, 0, time.UTC))),
						WebURL:      "https://example.com/gitlab/example-group/example-project/-/milestones/124",
					},
				}, nil, nil)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// GIVEN
			clientOpts := tc.clientOptions
			if len(clientOpts) == 0 {
				clientOpts = []gitlab.ClientOptionFunc{gitlab.WithBaseURL("https://gitlab.com/api/v4")}
			}
			testClient := gitlabtesting.NewTestClient(t, clientOpts...)
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

func TestGroupMilestoneList_JSON(t *testing.T) {
	t.Parallel()

	testMilestones := []*gitlab.GroupMilestone{
		{
			ID:          123,
			GroupID:     456,
			Title:       "Group milestone",
			Description: "Group description",
			State:       "active",
		},
		{
			ID:          124,
			ProjectID:   789,
			Title:       "Descendant project milestone",
			Description: "Project description",
			State:       "active",
		},
	}

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockGroupMilestones.EXPECT().
		ListGroupMilestones("456", &gitlab.ListGroupMilestonesOptions{
			ListOptions: gitlab.ListOptions{
				Page:    1,
				PerPage: 20,
			},
			IncludeDescendents: new(true),
		}).
		Return(testMilestones, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdList,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	out, err := exec("--group 456 --include-descendants --output json")
	require.NoError(t, err)

	var got []gitlab.GroupMilestone
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &got))
	require.Len(t, got, 2)
	assert.Equal(t, int64(456), got[0].GroupID)
	assert.Equal(t, int64(0), got[0].ProjectID)
	assert.Equal(t, "Group milestone", got[0].Title)
	assert.Equal(t, int64(0), got[1].GroupID)
	assert.Equal(t, int64(789), got[1].ProjectID)
	assert.Equal(t, "Descendant project milestone", got[1].Title)
	assert.Empty(t, out.Stderr())
}

func Test_milestoneOwner(t *testing.T) {
	tests := []struct {
		name      string
		milestone *gitlab.GroupMilestone
		basePath  string
		expected  string
	}{
		{
			name:      "nil milestone",
			milestone: nil,
			expected:  "",
		},
		{
			name: "group milestone with standard web URL",
			milestone: &gitlab.GroupMilestone{
				GroupID: 5,
				WebURL:  "https://gitlab.com/groups/gitlab-org/-/milestones/42",
			},
			expected: "gitlab-org",
		},
		{
			name: "subgroup milestone with standard web URL",
			milestone: &gitlab.GroupMilestone{
				GroupID: 15,
				WebURL:  "https://gitlab.com/groups/gitlab-org/subgroup/-/milestones/42",
			},
			expected: "gitlab-org/subgroup",
		},
		{
			name: "project milestone with standard web URL",
			milestone: &gitlab.GroupMilestone{
				ProjectID: 9,
				WebURL:    "https://gitlab.com/gitlab-org/gitlab/-/milestones/1",
			},
			expected: "gitlab-org/gitlab",
		},
		{
			name: "subgroup project milestone with standard web URL",
			milestone: &gitlab.GroupMilestone{
				ProjectID: 10,
				WebURL:    "https://gitlab.com/gitlab-org/subgroup/project/-/milestones/1",
			},
			expected: "gitlab-org/subgroup/project",
		},
		{
			name: "milestone with legacy web URL without dash",
			milestone: &gitlab.GroupMilestone{
				GroupID: 5,
				WebURL:  "https://gitlab.example.com/groups/gitlab-org/milestones/42",
			},
			expected: "gitlab-org",
		},
		{
			name: "group milestone on self-managed sub-path instance",
			milestone: &gitlab.GroupMilestone{
				GroupID: 5,
				WebURL:  "https://example.com/gitlab/groups/foo/-/milestones/1",
			},
			basePath: "/gitlab",
			expected: "foo",
		},
		{
			name: "project milestone on self-managed sub-path instance",
			milestone: &gitlab.GroupMilestone{
				ProjectID: 9,
				WebURL:    "https://example.com/gitlab/foo/proj/-/milestones/1",
			},
			basePath: "/gitlab",
			expected: "foo/proj",
		},
		{
			name: "nested subgroup milestone on self-managed sub-path instance",
			milestone: &gitlab.GroupMilestone{
				GroupID: 15,
				WebURL:  "https://example.com/gitlab/groups/foo/bar/-/milestones/1",
			},
			basePath: "/gitlab",
			expected: "foo/bar",
		},
		{
			name: "empty web URL fallback to project ID",
			milestone: &gitlab.GroupMilestone{
				ProjectID: 99,
			},
			expected: "project/99",
		},
		{
			name: "empty web URL fallback to group ID",
			milestone: &gitlab.GroupMilestone{
				GroupID: 88,
			},
			expected: "group/88",
		},
		{
			name:      "empty milestone with no IDs or web URL",
			milestone: &gitlab.GroupMilestone{},
			expected:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, milestoneOwner(tt.milestone, tt.basePath))
		})
	}
}

func Test_instanceBasePath(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{
			name:     "root gitlab instance",
			baseURL:  "https://gitlab.com/api/v4/",
			expected: "",
		},
		{
			name:     "sub-path gitlab instance",
			baseURL:  "https://example.com/gitlab/api/v4/",
			expected: "/gitlab",
		},
		{
			name:     "nested sub-path gitlab instance",
			baseURL:  "https://example.com/sub/gitlab/api/v4/",
			expected: "/sub/gitlab",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := gitlab.NewClient("", gitlab.WithBaseURL(tt.baseURL))
			require.NoError(t, err)
			assert.Equal(t, tt.expected, instanceBasePath(client))
		})
	}
}
