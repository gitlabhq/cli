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
