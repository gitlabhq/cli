package api

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

// ListOptions selects the timelogs to list. Set exactly one of Group and
// Project. Zero dates and an empty Username leave that filter off.
type ListOptions struct {
	Group     string
	Project   string
	StartDate time.Time
	EndDate   time.Time
	Username  string
}

// Timelog is one entry of time logged on an issue or a merge request.
// TimeSpent is in seconds, and is negative for time that was subtracted.
type Timelog struct {
	SpentAt      time.Time `json:"spentAt"`
	TimeSpent    int64     `json:"timeSpent"`
	Summary      string    `json:"summary"`
	User         User      `json:"user"`
	Project      Project   `json:"project"`
	Issue        *Issuable `json:"issue"`
	MergeRequest *Issuable `json:"mergeRequest"`
}

type User struct {
	Username string `json:"username"`
}

type Project struct {
	FullPath string `json:"fullPath"`
}

// Issuable is the issue or merge request a timelog was logged on.
type Issuable struct {
	IID    string `json:"iid"`
	Title  string `json:"title"`
	WebURL string `json:"webUrl"`
}

type PageInfo struct {
	EndCursor   string `json:"endCursor"`
	HasNextPage bool   `json:"hasNextPage"`
}

type TimelogConnection struct {
	Nodes    []Timelog `json:"nodes"`
	PageInfo PageInfo  `json:"pageInfo"`
}

type Namespace struct {
	Timelogs *TimelogConnection `json:"timelogs"`
}

// TimelogsResponse is the GraphQL response for one page of timelogs. Argument
// errors arrive in Errors with an HTTP 200, so GraphQL.Do does not report them.
type TimelogsResponse struct {
	Data struct {
		Group   *Namespace `json:"group"`
		Project *Namespace `json:"project"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

const timelogsQuery = `
query ListTimelogs($fullPath: ID!, $startDate: Time, $endDate: Time, $username: String, $after: String) {
	%s(fullPath: $fullPath) {
		timelogs(startDate: $startDate, endDate: $endDate, username: $username, sort: SPENT_AT_DESC, first: 100, after: $after) {
			nodes {
				spentAt
				timeSpent
				summary
				user {
					username
				}
				project {
					fullPath
				}
				issue {
					iid
					title
					webUrl
				}
				mergeRequest {
					iid
					title
					webUrl
				}
			}
			pageInfo {
				endCursor
				hasNextPage
			}
		}
	}
}
`

// ListTimelogs yields every timelog matching opts, newest first, fetching
// further pages as the caller ranges over it.
//
// GitLab drops timelogs the user cannot see from a page, but not from the
// page count, so a page can be empty and still have a next page.
func ListTimelogs(ctx context.Context, client *gitlab.Client, opts ListOptions) iter.Seq2[Timelog, error] {
	return func(yield func(Timelog, error) bool) {
		after := ""
		for {
			conn, err := listTimelogsPage(ctx, client, opts, after)
			if err != nil {
				yield(Timelog{}, err)
				return
			}
			for _, timelog := range conn.Nodes {
				if !yield(timelog, nil) {
					return
				}
			}
			if !conn.PageInfo.HasNextPage || conn.PageInfo.EndCursor == "" {
				return
			}
			after = conn.PageInfo.EndCursor
		}
	}
}

func listTimelogsPage(ctx context.Context, client *gitlab.Client, opts ListOptions, after string) (*TimelogConnection, error) {
	field, fullPath := "project", opts.Project
	if opts.Group != "" {
		field, fullPath = "group", opts.Group
	}

	variables := map[string]any{"fullPath": fullPath}
	if !opts.StartDate.IsZero() {
		variables["startDate"] = opts.StartDate.Format(time.DateOnly)
	}
	if !opts.EndDate.IsZero() {
		variables["endDate"] = opts.EndDate.Format(time.DateOnly)
	}
	if opts.Username != "" {
		variables["username"] = opts.Username
	}
	if after != "" {
		variables["after"] = after
	}

	query := gitlab.GraphQLQuery{
		Query:     fmt.Sprintf(timelogsQuery, field),
		Variables: variables,
	}

	var response TimelogsResponse
	if _, err := client.GraphQL.Do(query, &response, gitlab.WithContext(ctx)); err != nil {
		return nil, fmt.Errorf("failed to list timelogs: %w", err)
	}
	if len(response.Errors) > 0 {
		errs := make([]error, 0, len(response.Errors))
		for _, e := range response.Errors {
			errs = append(errs, errors.New(e.Message))
		}
		return nil, fmt.Errorf("failed to list timelogs: %w", errors.Join(errs...))
	}

	namespace := response.Data.Project
	if opts.Group != "" {
		namespace = response.Data.Group
	}
	if namespace == nil || namespace.Timelogs == nil {
		return nil, fmt.Errorf("%s not found: %s", field, fullPath)
	}
	return namespace.Timelogs, nil
}
