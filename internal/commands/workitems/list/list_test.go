//go:build !integration

package list

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	workitemsapi "gitlab.com/gitlab-org/cli/internal/commands/workitems/api"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestWorkItemsList(t *testing.T) {
	tests := []struct {
		name       string
		args       string
		setupMock  func(tc *gitlabtesting.TestClient)
		wantErr    bool
		wantOutput string
	}{
		{
			name: "lists work items in project",
			args: "",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGraphQL.EXPECT().
					Do(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(query gitlab.GraphQLQuery, response any, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
						// Type assert to the exact structure used in api.go
						resp, _ := response.(*workitemsapi.WorkItemsResponse)

						// Populate mock response with project work items
						resp.Data.Project = &workitemsapi.ProjectWorkItems{
							WorkItems: workitemsapi.WorkItemsConnection{
								Nodes: []workitemsapi.WorkItem{
									{
										IID:   "1",
										Title: "Implement new feature",
										State: "OPEN",
										WorkItemType: struct {
											Name string `json:"name"`
										}{
											Name: "Issue",
										},
										Author: struct {
											Username string `json:"username"`
										}{
											Username: "testuser",
										},
										WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1",
									},
									{
										IID:   "2",
										Title: "Fix critical bug",
										State: "CLOSED",
										WorkItemType: struct {
											Name string `json:"name"`
										}{
											Name: "Issue",
										},
										Author: struct {
											Username string `json:"username"`
										}{
											Username: "anotheruser",
										},
										WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/2",
									},
								},
								PageInfo: workitemsapi.PageInfo{
									EndCursor:   "",
									HasNextPage: false,
								},
							},
						}

						return &gitlab.Response{}, nil
					})
			},
			wantOutput: "Implement new feature",
		},
		{
			name: "lists work items in group",
			args: "--group test-group",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGraphQL.EXPECT().
					Do(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(query gitlab.GraphQLQuery, response any, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
						resp, _ := response.(*workitemsapi.WorkItemsResponse)

						// Populate mock response with group work items
						resp.Data.Group = &workitemsapi.GroupWorkItems{
							WorkItems: workitemsapi.WorkItemsConnection{
								Nodes: []workitemsapi.WorkItem{
									{
										IID:   "1",
										Title: "Epic for Q1",
										State: "OPEN",
										WorkItemType: struct {
											Name string `json:"name"`
										}{
											Name: "Epic",
										},
										Author: struct {
											Username string `json:"username"`
										}{
											Username: "groupowner",
										},
										WebURL: "https://gitlab.com/groups/test-group/-/epics/1",
									},
								},
								PageInfo: workitemsapi.PageInfo{
									EndCursor:   "",
									HasNextPage: false,
								},
							},
						}

						return &gitlab.Response{}, nil
					})
			},
			wantOutput: "Epic for Q1",
		},
		{
			name: "empty work items list",
			args: "",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGraphQL.EXPECT().
					Do(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(query gitlab.GraphQLQuery, response any, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
						resp, _ := response.(*workitemsapi.WorkItemsResponse)

						resp.Data.Project = &workitemsapi.ProjectWorkItems{
							WorkItems: workitemsapi.WorkItemsConnection{
								Nodes:    []workitemsapi.WorkItem{},
								PageInfo: workitemsapi.PageInfo{HasNextPage: false},
							},
						}

						return &gitlab.Response{}, nil
					})
			},
			wantOutput: "No work items found in OWNER/REPO",
		},
		{
			name: "filters by work item type",
			args: "--type epic",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGraphQL.EXPECT().
					Do(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(query gitlab.GraphQLQuery, response any, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
						// Verify that types filter is passed in query variables
						assert.Contains(t, query.Variables, "types")
						types, ok := query.Variables["types"].([]string)
						require.True(t, ok)
						assert.Equal(t, []string{"EPIC"}, types)

						resp, _ := response.(*workitemsapi.WorkItemsResponse)

						resp.Data.Project = &workitemsapi.ProjectWorkItems{
							WorkItems: workitemsapi.WorkItemsConnection{
								Nodes: []workitemsapi.WorkItem{
									{
										IID:   "1",
										Title: "Q1 Planning Epic",
										State: "OPEN",
										WorkItemType: struct {
											Name string `json:"name"`
										}{
											Name: "Epic",
										},
										Author: struct {
											Username string `json:"username"`
										}{
											Username: "epicowner",
										},
										WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1",
									},
								},
								PageInfo: workitemsapi.PageInfo{
									HasNextPage: false,
								},
							},
						}

						return &gitlab.Response{}, nil
					})
			},
			wantOutput: "Q1 Planning Epic",
		},
		{
			name: "json output format",
			args: "--output json",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGraphQL.EXPECT().
					Do(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(query gitlab.GraphQLQuery, response any, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
						resp, _ := response.(*workitemsapi.WorkItemsResponse)

						resp.Data.Project = &workitemsapi.ProjectWorkItems{
							WorkItems: workitemsapi.WorkItemsConnection{
								Nodes: []workitemsapi.WorkItem{
									{
										IID:   "1",
										Title: "Test Item",
										State: "OPEN",
										WorkItemType: struct {
											Name string `json:"name"`
										}{
											Name: "Issue",
										},
										Author: struct {
											Username string `json:"username"`
										}{
											Username: "testuser",
										},
										WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1",
									},
								},
								PageInfo: workitemsapi.PageInfo{
									HasNextPage: false,
								},
							},
						}

						return &gitlab.Response{}, nil
					})
			},
			wantOutput: `"iid":"1"`,
		},
		{
			name: "handles pagination with cursor",
			args: "--after cursor123",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGraphQL.EXPECT().
					Do(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(query gitlab.GraphQLQuery, response any, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
						// verify cursor was passed
						assert.Equal(t, "cursor123", query.Variables["after"])

						resp, _ := response.(*workitemsapi.WorkItemsResponse)
						resp.Data.Project = &workitemsapi.ProjectWorkItems{
							WorkItems: workitemsapi.WorkItemsConnection{
								Nodes: []workitemsapi.WorkItem{
									{
										IID:   "2",
										Title: "Second page item",
										State: "OPEN",
										WorkItemType: struct {
											Name string `json:"name"`
										}{
											Name: "Issue",
										},
										Author: struct {
											Username string `json:"username"`
										}{
											Username: "user2",
										},
										WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/2",
									},
								},
								PageInfo: workitemsapi.PageInfo{
									EndCursor:   "cursor456",
									HasNextPage: true,
								},
							},
						}
						return &gitlab.Response{}, nil
					})
			},
			wantOutput: `Next page: glab work-items list --after "cursor456"`,
		},
		{
			name: "filters by state - closed",
			args: "--state closed",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGraphQL.EXPECT().
					Do(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(query gitlab.GraphQLQuery, response any, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
						// Verify state filter is passed
						assert.Contains(t, query.Variables, "state")
						assert.Equal(t, "closed", query.Variables["state"])

						resp, _ := response.(*workitemsapi.WorkItemsResponse)

						resp.Data.Project = &workitemsapi.ProjectWorkItems{
							WorkItems: workitemsapi.WorkItemsConnection{
								Nodes: []workitemsapi.WorkItem{
									{
										IID:   "1",
										Title: "Closed issue",
										State: "CLOSED",
										WorkItemType: struct {
											Name string `json:"name"`
										}{
											Name: "Issue",
										},
										Author: struct {
											Username string `json:"username"`
										}{
											Username: "testuser",
										},
										WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1",
									},
								},
								PageInfo: workitemsapi.PageInfo{HasNextPage: false},
							},
						}

						return &gitlab.Response{}, nil
					})
			},
			wantOutput: "Closed issue",
		},
		{
			name: "filters by state - all (no state filter sent)",
			args: "--state all",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGraphQL.EXPECT().
					Do(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(query gitlab.GraphQLQuery, response any, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
						// Verify state filter is NOT passed when state is "all"
						assert.NotContains(t, query.Variables, "state")

						resp, _ := response.(*workitemsapi.WorkItemsResponse)

						resp.Data.Project = &workitemsapi.ProjectWorkItems{
							WorkItems: workitemsapi.WorkItemsConnection{
								Nodes: []workitemsapi.WorkItem{
									{
										IID:   "1",
										Title: "Open issue",
										State: "OPEN",
										WorkItemType: struct {
											Name string `json:"name"`
										}{
											Name: "Issue",
										},
										Author: struct {
											Username string `json:"username"`
										}{
											Username: "user1",
										},
										WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1",
									},
									{
										IID:   "2",
										Title: "Closed issue",
										State: "CLOSED",
										WorkItemType: struct {
											Name string `json:"name"`
										}{
											Name: "Issue",
										},
										Author: struct {
											Username string `json:"username"`
										}{
											Username: "user2",
										},
										WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/2",
									},
								},
								PageInfo: workitemsapi.PageInfo{HasNextPage: false},
							},
						}

						return &gitlab.Response{}, nil
					})
			},
			wantOutput: "Open issue",
		},
		{
			name: "default state is opened",
			args: "",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGraphQL.EXPECT().
					Do(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(query gitlab.GraphQLQuery, response any, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
						// Verify default state "opened" is passed
						assert.Contains(t, query.Variables, "state")
						assert.Equal(t, "opened", query.Variables["state"])

						resp, _ := response.(*workitemsapi.WorkItemsResponse)

						resp.Data.Project = &workitemsapi.ProjectWorkItems{
							WorkItems: workitemsapi.WorkItemsConnection{
								Nodes: []workitemsapi.WorkItem{
									{
										IID:   "1",
										Title: "Open issue",
										State: "OPEN",
										WorkItemType: struct {
											Name string `json:"name"`
										}{
											Name: "Issue",
										},
										Author: struct {
											Username string `json:"username"`
										}{
											Username: "user1",
										},
										WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1",
									},
								},
								PageInfo: workitemsapi.PageInfo{HasNextPage: false},
							},
						}

						return &gitlab.Response{}, nil
					})
			},
			wantOutput: "Open issue",
		},
		{
			name:    "handles API error",
			args:    "",
			wantErr: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGraphQL.EXPECT().
					Do(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, assert.AnError)
			},
		},
		{
			name:    "handles project not found",
			args:    "",
			wantErr: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGraphQL.EXPECT().
					Do(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(query gitlab.GraphQLQuery, response any, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
						resp, _ := response.(*workitemsapi.WorkItemsResponse)

						// Return nil project to trigger "project not found" error
						resp.Data.Project = nil

						return &gitlab.Response{}, nil
					})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test client with mocks
			tc := gitlabtesting.NewTestClient(t)
			tt.setupMock(tc)

			// Setup command for test
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			)

			// Execute command
			out, err := exec(tt.args)

			// Assertions
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Contains(t, out.OutBuf.String(), tt.wantOutput)
			}
		})
	}
}

func TestWorkItemsList_FlagValidation(t *testing.T) {
	// NOTE: No t.Parallel() here due to Viper global state issues

	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{
			name:    "invalid output format",
			args:    "--output xml",
			wantErr: "must be one of",
		},
		{
			name:    "empty type in comma-separated list",
			args:    "--type epic,",
			wantErr: "empty work item type",
		},
		{
			name:    "whitespace only type",
			args:    "--type '  '",
			wantErr: "empty work item type",
		},
		{
			name:    "invalid per-page value - too high",
			args:    "--per-page 101",
			wantErr: "--per-page must be between 1 and 100",
		},
		{
			name:    "invalid per-page value - too low",
			args:    "--per-page 0",
			wantErr: "--per-page must be between 1 and 100",
		},
		{
			name:    "invalid page value",
			args:    "--page 0",
			wantErr: "unknown flag: --page",
		},
		{
			name:    "invalid state value",
			args:    "--state invalid",
			wantErr: "--state must be one of: opened, closed, all",
		},
		{
			name:    "invalid relative date unit",
			args:    "--created-after -7h",
			wantErr: `invalid argument "-7h" for "--created-after" flag`,
		},
		{
			name:    "relative date amount out of range",
			args:    "--due-before 10000d",
			wantErr: "relative date amount must be between -9999 and 9999",
		},
		{
			name:    "invalid absolute date",
			args:    "--updated-before 2026-02-30",
			wantErr: `invalid argument "2026-02-30" for "--updated-before" flag`,
		},
		{
			name:    "closed date filter with opened state",
			args:    "--state opened --closed-after -7d",
			wantErr: "--closed-after and --closed-before need --state closed or all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			)

			_, err := exec(tt.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestWorkItemsList_DateFilters(t *testing.T) {
	tests := []struct {
		name       string
		args       string
		assertVars func(t *testing.T, query gitlab.GraphQLQuery)
		group      bool
		hasNext    bool
		wantOutput string
	}{
		{
			name: "no date filters",
			args: "",
			assertVars: func(t *testing.T, query gitlab.GraphQLQuery) {
				t.Helper()
				assert.NotContains(t, query.Query, "relativeDateFilters")
				assert.NotContains(t, query.Query, "createdAfter")
				assert.NotContains(t, query.Variables, "relativeDateFilters")
			},
		},
		{
			name: "relative date filters",
			args: "--created-after -7d --due-before 2w",
			assertVars: func(t *testing.T, query gitlab.GraphQLQuery) {
				t.Helper()
				assert.Contains(t, query.Query, "$relativeDateFilters: WorkItemRelativeDateFiltersInput")
				assert.Contains(t, query.Query, "relativeDateFilters: $relativeDateFilters")
				assert.Equal(t, map[workitemsapi.DateField]workitemsapi.RelativeDate{
					workitemsapi.CreatedAfter: {Amount: -7, Unit: workitemsapi.RelativeDateUnitDay},
					workitemsapi.DueBefore:    {Amount: 2, Unit: workitemsapi.RelativeDateUnitWeek},
				}, query.Variables["relativeDateFilters"])
				assert.NotContains(t, query.Variables, "createdAfter")
			},
		},
		{
			name: "absolute date filters",
			args: "--closed-after 2026-01-01 --updated-before 2026-04-01T12:00:00Z",
			assertVars: func(t *testing.T, query gitlab.GraphQLQuery) {
				t.Helper()
				assert.Contains(t, query.Query, "$closedAfter: Time")
				assert.Contains(t, query.Query, "closedAfter: $closedAfter")
				assert.Equal(t, "2026-01-01", query.Variables["closedAfter"])
				assert.Equal(t, "2026-04-01T12:00:00Z", query.Variables["updatedBefore"])
				assert.NotContains(t, query.Variables, "relativeDateFilters")
			},
		},
		{
			name: "all absolute date flags map to their fields",
			args: "--state all --created-after 2026-01-01 --created-before 2026-01-02 --updated-after 2026-01-03 --updated-before 2026-01-04 " +
				"--closed-after 2026-01-05 --closed-before 2026-01-06 --due-after 2026-01-07 --due-before 2026-01-08",
			assertVars: func(t *testing.T, query gitlab.GraphQLQuery) {
				t.Helper()
				assert.Equal(t, map[string]any{
					"projectPath":   "OWNER/REPO",
					"first":         int64(20),
					"createdAfter":  "2026-01-01",
					"createdBefore": "2026-01-02",
					"updatedAfter":  "2026-01-03",
					"updatedBefore": "2026-01-04",
					"closedAfter":   "2026-01-05",
					"closedBefore":  "2026-01-06",
					"dueAfter":      "2026-01-07",
					"dueBefore":     "2026-01-08",
				}, query.Variables)
			},
		},
		{
			name: "all relative date flags map to their fields",
			args: "--state all --created-after -1d --created-before -2d --updated-after -3d --updated-before -4d " +
				"--closed-after -5d --closed-before -6d --due-after 7d --due-before 8d",
			assertVars: func(t *testing.T, query gitlab.GraphQLQuery) {
				t.Helper()
				assert.Equal(t, map[string]any{
					"projectPath": "OWNER/REPO",
					"first":       int64(20),
					"relativeDateFilters": map[workitemsapi.DateField]workitemsapi.RelativeDate{
						workitemsapi.CreatedAfter:  {Amount: -1, Unit: workitemsapi.RelativeDateUnitDay},
						workitemsapi.CreatedBefore: {Amount: -2, Unit: workitemsapi.RelativeDateUnitDay},
						workitemsapi.UpdatedAfter:  {Amount: -3, Unit: workitemsapi.RelativeDateUnitDay},
						workitemsapi.UpdatedBefore: {Amount: -4, Unit: workitemsapi.RelativeDateUnitDay},
						workitemsapi.ClosedAfter:   {Amount: -5, Unit: workitemsapi.RelativeDateUnitDay},
						workitemsapi.ClosedBefore:  {Amount: -6, Unit: workitemsapi.RelativeDateUnitDay},
						workitemsapi.DueAfter:      {Amount: 7, Unit: workitemsapi.RelativeDateUnitDay},
						workitemsapi.DueBefore:     {Amount: 8, Unit: workitemsapi.RelativeDateUnitDay},
					},
				}, query.Variables)
			},
		},
		{
			name: "closed date filter implies closed state",
			args: "--closed-before -1d",
			assertVars: func(t *testing.T, query gitlab.GraphQLQuery) {
				t.Helper()
				assert.Equal(t, "closed", query.Variables["state"])
			},
		},
		{
			name: "closed date filter keeps explicit all state",
			args: "--state all --closed-after -7d",
			assertVars: func(t *testing.T, query gitlab.GraphQLQuery) {
				t.Helper()
				assert.NotContains(t, query.Variables, "state")
			},
		},
		{
			name:  "group scope date filters",
			args:  "-g test-group --created-after -7d --closed-before 2026-04-01",
			group: true,
			assertVars: func(t *testing.T, query gitlab.GraphQLQuery) {
				t.Helper()
				assert.Contains(t, query.Query, "query ListGroupWorkItems(")
				assert.Contains(t, query.Query, "$closedBefore: Time")
				assert.Contains(t, query.Query, "closedBefore: $closedBefore")
				assert.Contains(t, query.Query, "$relativeDateFilters: WorkItemRelativeDateFiltersInput")
				assert.Contains(t, query.Query, "relativeDateFilters: $relativeDateFilters")
				assert.Equal(t, "2026-04-01", query.Variables["closedBefore"])
				assert.Equal(t, map[workitemsapi.DateField]workitemsapi.RelativeDate{
					workitemsapi.CreatedAfter: {Amount: -7, Unit: workitemsapi.RelativeDateUnitDay},
				}, query.Variables["relativeDateFilters"])
			},
		},
		{
			name:    "next page command keeps relative dates",
			args:    "--updated-after -1y --updated-before -1m",
			hasNext: true,
			assertVars: func(t *testing.T, query gitlab.GraphQLQuery) {
				t.Helper()
				assert.Contains(t, query.Variables, "relativeDateFilters")
			},
			wantOutput: `Next page: glab work-items list --updated-after -1y --updated-before -1m --after "cursor456"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t)
			tc.MockGraphQL.EXPECT().
				Do(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(query gitlab.GraphQLQuery, response any, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
					tt.assertVars(t, query)

					conn := workitemsapi.WorkItemsConnection{
						Nodes: []workitemsapi.WorkItem{
							{IID: "1", Title: "Recent item", State: "OPEN"},
						},
						PageInfo: workitemsapi.PageInfo{EndCursor: "cursor456", HasNextPage: tt.hasNext},
					}
					resp, _ := response.(*workitemsapi.WorkItemsResponse)
					if tt.group {
						resp.Data.Group = &workitemsapi.GroupWorkItems{WorkItems: conn}
					} else {
						resp.Data.Project = &workitemsapi.ProjectWorkItems{WorkItems: conn}
					}
					return &gitlab.Response{}, nil
				})

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			)

			out, err := exec(tt.args)

			require.NoError(t, err)
			assert.Contains(t, out.OutBuf.String(), "Recent item")
			if tt.wantOutput != "" {
				assert.Contains(t, out.OutBuf.String(), tt.wantOutput)
			}
		})
	}
}
