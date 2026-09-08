package api

import (
	"context"
	"fmt"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

// scope type constants
const (
	ScopeTypeGroup   = "group"
	ScopeTypeProject = "project"
)

// ScopeInfo contains detected scope information for work items queries
type ScopeInfo struct {
	Type string
	Path string
}

// WorkItem represents a work item from the GraphQL API
type WorkItem struct {
	IID          string `json:"iid"`
	Title        string `json:"title"`
	State        string `json:"state"`
	WorkItemType struct {
		Name string `json:"name"`
	} `json:"workItemType"`
	Author struct {
		Username string `json:"username"`
	} `json:"author"`
	WebURL string `json:"webUrl"`
}

// GraphQL query templates
const (
	groupWorkItemsQuery = `
	query ListGroupWorkItems($groupPath: ID!, $types: [IssueType!], $state: IssuableState, $first: Int, $after: String) {
	group(fullPath: $groupPath) {
		workItems(types: $types, state: $state, first: $first, after: $after) {
			nodes {
				iid
				title
				state
				workItemType {
					name
				}
				author {
					username
				}
				webUrl
			}
			pageInfo {
				endCursor
				hasNextPage
			}
		}
	}
}
`

	projectWorkItemsQuery = `
	query ListProjectWorkItems($projectPath: ID!, $types: [IssueType!], $state: IssuableState, $first: Int, $after: String) {
	project(fullPath: $projectPath) {
		workItems(types: $types, state: $state, first: $first, after: $after) {
			nodes {
				iid
				title
				state
				workItemType {
					name
				}
				author {
					username
				}
				webUrl
			}
			pageInfo {
				endCursor
				hasNextPage
			}
		}
	}
}
`
)

// FetchWorkItems retrieves all work items using cursor-based pagination
func FetchWorkItems(ctx context.Context, client *gitlab.Client, scope *ScopeInfo, types []string, state string, after string, perPage int64) ([]WorkItem, *PageInfo, error) {
	var queryStr string
	var pathKey string

	switch scope.Type {
	case ScopeTypeGroup:
		queryStr = groupWorkItemsQuery
		pathKey = "groupPath"
	case ScopeTypeProject:
		queryStr = projectWorkItemsQuery
		pathKey = "projectPath"
	default:
		return nil, nil, fmt.Errorf("invalid scope type: %s", scope.Type)
	}

	// uppercase types for GraphQL API (API exepcts EPIC not epic)
	var uppercaseTypes []string
	if len(types) > 0 {
		uppercaseTypes = make([]string, len(types))
		for i, t := range types {
			uppercaseTypes[i] = strings.ToUpper(strings.TrimSpace(t))
		}
	}

	// Build query vars
	variables := map[string]any{
		pathKey: scope.Path,
		"first": perPage, // user-specified page size (max 100)
	}

	if after != "" {
		variables["after"] = after
	}

	// add types filter if specified
	if len(uppercaseTypes) > 0 {
		variables["types"] = uppercaseTypes
	}

	// add state filter
	if state != "all" {
		variables["state"] = state
	}

	query := gitlab.GraphQLQuery{
		Query:     queryStr,
		Variables: variables,
	}

	var response WorkItemsResponse

	// execute query
	_, err := client.GraphQL.Do(query, &response, gitlab.WithContext(ctx))
	if err != nil {
		return nil, nil, fmt.Errorf("GraphQL query failed: %w", err)
	}

	// Extract work items based on scope
	var nodes []WorkItem
	var pageInfo PageInfo

	if scope.Type == ScopeTypeGroup {
		if response.Data.Group == nil {
			return nil, nil, fmt.Errorf("group not found: %s", scope.Path)
		}
		nodes = response.Data.Group.WorkItems.Nodes
		pageInfo = response.Data.Group.WorkItems.PageInfo
	} else {
		if response.Data.Project == nil {
			return nil, nil, fmt.Errorf("project not found: %s", scope.Path)
		}
		nodes = response.Data.Project.WorkItems.Nodes
		pageInfo = response.Data.Project.WorkItems.PageInfo
	}

	return nodes, &pageInfo, nil
}

// WorkItemsResponse represents the GraphQL response structure for work items queries
type WorkItemsResponse struct {
	Data struct {
		Group   *GroupWorkItems   `json:"group,omitempty"`
		Project *ProjectWorkItems `json:"project,omitempty"`
	} `json:"data"`
}

// helper structs for GraphQL response parsing
type GroupWorkItems struct {
	WorkItems WorkItemsConnection `json:"workItems"`
}

type ProjectWorkItems struct {
	WorkItems WorkItemsConnection `json:"workItems"`
}

type WorkItemsConnection struct {
	Nodes    []WorkItem `json:"nodes"`
	PageInfo PageInfo   `json:"pageInfo"`
}

type PageInfo struct {
	EndCursor   string `json:"endCursor"`
	HasNextPage bool   `json:"hasNextPage"`
}
