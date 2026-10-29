package api

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	glabapi "gitlab.com/gitlab-org/cli/internal/api"
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

// DateField is a work item date filter argument, named as in the GraphQL API.
type DateField string

const (
	CreatedAfter  DateField = "createdAfter"
	CreatedBefore DateField = "createdBefore"
	UpdatedAfter  DateField = "updatedAfter"
	UpdatedBefore DateField = "updatedBefore"
	ClosedAfter   DateField = "closedAfter"
	ClosedBefore  DateField = "closedBefore"
	DueAfter      DateField = "dueAfter"
	DueBefore     DateField = "dueBefore"
)

// RelativeDateUnit is a WorkItemRelativeDateUnit enum value.
type RelativeDateUnit string

const (
	RelativeDateUnitDay   RelativeDateUnit = "DAY"
	RelativeDateUnitWeek  RelativeDateUnit = "WEEK"
	RelativeDateUnitMonth RelativeDateUnit = "MONTH"
	RelativeDateUnitYear  RelativeDateUnit = "YEAR"
)

// RelativeDate is an offset from today. Negative amounts are in the past.
type RelativeDate struct {
	Amount int              `json:"amount"`
	Unit   RelativeDateUnit `json:"unit"`
}

// DateFilter holds exactly one of an absolute or a relative date. Absolute is
// sent as typed: the Time scalar parses it with Ruby's Time.parse, so a date
// without an offset is read in the system time zone of the server.
type DateFilter struct {
	Absolute string
	Relative *RelativeDate
}

const maxRelativeDateAmount = 9999

var (
	relativeDateRe    = regexp.MustCompile(`^([+-]?\d+)([dwmy])$`)
	relativeDateUnits = map[string]RelativeDateUnit{
		"d": RelativeDateUnitDay,
		"w": RelativeDateUnitWeek,
		"m": RelativeDateUnitMonth,
		"y": RelativeDateUnitYear,
	}
)

// ParseDateFilter parses an absolute date (2006-01-02 or RFC 3339) or a
// relative date such as -7d, 2w, -1m, or 1y.
func ParseDateFilter(s string) (DateFilter, error) {
	if m := relativeDateRe.FindStringSubmatch(s); m != nil {
		amount, err := strconv.Atoi(m[1])
		if err != nil || amount < -maxRelativeDateAmount || amount > maxRelativeDateAmount {
			return DateFilter{}, fmt.Errorf("relative date amount must be between -%[1]d and %[1]d", maxRelativeDateAmount)
		}
		return DateFilter{Relative: &RelativeDate{Amount: amount, Unit: relativeDateUnits[m[2]]}}, nil
	}

	for _, layout := range []string{time.DateOnly, time.RFC3339} {
		if _, err := time.Parse(layout, s); err == nil {
			return DateFilter{Absolute: s}, nil
		}
	}

	return DateFilter{}, fmt.Errorf("must be a date (YYYY-MM-DD), an RFC 3339 timestamp, or a relative date such as -7d, 2w, -1m, or 1y")
}

// ListOptions are the filters and pagination for FetchWorkItems.
type ListOptions struct {
	Types   []string
	State   string
	After   string
	PerPage int64
	Dates   map[DateField]DateFilter
}

// GraphQL query templates. Date filter arguments are added only when set,
// so that instances without them still accept the query.
const (
	groupWorkItemsQuery = `
	query ListGroupWorkItems($groupPath: ID!, $types: [IssueType!], $state: IssuableState, $first: Int, $after: String%[1]s) {
	group(fullPath: $groupPath) {
		workItems(types: $types, state: $state, first: $first, after: $after%[2]s) {
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
	query ListProjectWorkItems($projectPath: ID!, $types: [IssueType!], $state: IssuableState, $first: Int, $after: String%[1]s) {
	project(fullPath: $projectPath) {
		workItems(types: $types, state: $state, first: $first, after: $after%[2]s) {
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
func FetchWorkItems(ctx context.Context, client *gitlab.Client, scope *ScopeInfo, opts ListOptions) ([]WorkItem, *PageInfo, error) {
	var queryTemplate string
	var pathKey string

	switch scope.Type {
	case ScopeTypeGroup:
		queryTemplate = groupWorkItemsQuery
		pathKey = "groupPath"
	case ScopeTypeProject:
		queryTemplate = projectWorkItemsQuery
		pathKey = "projectPath"
	default:
		return nil, nil, fmt.Errorf("invalid scope type: %s", scope.Type)
	}

	// uppercase types for GraphQL API (API exepcts EPIC not epic)
	var uppercaseTypes []string
	if len(opts.Types) > 0 {
		uppercaseTypes = make([]string, len(opts.Types))
		for i, t := range opts.Types {
			uppercaseTypes[i] = strings.ToUpper(strings.TrimSpace(t))
		}
	}

	// Build query vars
	variables := map[string]any{
		pathKey: scope.Path,
		"first": opts.PerPage, // user-specified page size (max 100)
	}

	if opts.After != "" {
		variables["after"] = opts.After
	}

	// add types filter if specified
	if len(uppercaseTypes) > 0 {
		variables["types"] = uppercaseTypes
	}

	// add state filter
	if opts.State != "all" {
		variables["state"] = opts.State
	}

	decls, args := addDateFilterVariables(variables, opts.Dates)

	query := gitlab.GraphQLQuery{
		Query:     fmt.Sprintf(queryTemplate, decls, args),
		Variables: variables,
	}

	var response WorkItemsResponse

	// execute query
	_, err := client.GraphQL.Do(query, &response, gitlab.WithContext(ctx), glabapi.WithSafeRetries())
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

// addDateFilterVariables adds the date filters to variables and returns the
// matching variable declarations and field arguments for the query.
func addDateFilterVariables(variables map[string]any, dates map[DateField]DateFilter) (string, string) {
	var decls, args strings.Builder
	relative := map[DateField]RelativeDate{}

	for _, field := range slices.Sorted(maps.Keys(dates)) {
		filter := dates[field]
		switch {
		case filter.Relative != nil:
			relative[field] = *filter.Relative
		case filter.Absolute != "":
			variables[string(field)] = filter.Absolute
			fmt.Fprintf(&decls, ", $%s: Time", field)    //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
			fmt.Fprintf(&args, ", %[1]s: $%[1]s", field) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
		}
	}

	if len(relative) > 0 {
		variables["relativeDateFilters"] = relative
		decls.WriteString(", $relativeDateFilters: WorkItemRelativeDateFiltersInput")
		args.WriteString(", relativeDateFilters: $relativeDateFilters")
	}

	return decls.String(), args.String()
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
