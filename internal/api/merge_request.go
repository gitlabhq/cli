package api

import (
	"sort"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

// GetMR returns an MR
// Attention: this is a global variable and may be overridden in tests.
var GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
	mr, _, err := client.MergeRequests.GetMergeRequest(projectID, mrID, opts)
	if err != nil {
		return nil, err
	}

	return mr, nil
}

// ListGroupMRs retrieves merge requests for a given group with optional filtering by assignees or reviewers.
//
// Parameters:
//   - client: A GitLab client instance.
//   - groupID: The ID or name of the group.
//   - opts: GitLab-specific options for listing group merge requests.
//   - listOpts: Optional list of arguments to filter by assignees or reviewers.
//     May be any combination of api.WithMRAssignees and api.WithMRReviewers.
//
// Returns:
//   - A slice of GitLab merge request objects and an error, if any.
//
// Example usage:
//
//	groupMRs, err := api.ListGroupMRs(client, "my-group", &gitlab.ListGroupMergeRequestsOptions{},
//		api.WithMRAssignees([]int{123}),
//		api.WithMRReviewers([]int{456, 789}))
func ListGroupMRs(client *gitlab.Client, projectID any, opts *gitlab.ListGroupMergeRequestsOptions, listOpts ...CliListMROption) ([]*gitlab.BasicMergeRequest, error) {
	composedListOpts := composeCliListMROptions(listOpts...)
	assigneeIds, reviewerIds := composedListOpts.assigneeIds, composedListOpts.reviewerIds

	if len(assigneeIds) > 0 || len(reviewerIds) > 0 {
		return listGroupMRsWithAssigneesOrReviewers(client, projectID, opts, assigneeIds, reviewerIds)
	} else {
		return listGroupMRsBase(client, projectID, opts)
	}
}

func listGroupMRsBase(client *gitlab.Client, groupID any, opts *gitlab.ListGroupMergeRequestsOptions) ([]*gitlab.BasicMergeRequest, error) {
	if opts.PerPage == 0 {
		opts.PerPage = DefaultListLimit
	}

	mrs, _, err := client.MergeRequests.ListGroupMergeRequests(groupID, opts)
	if err != nil {
		return nil, err
	}
	return mrs, nil
}

func listGroupMRsWithAssigneesOrReviewers(client *gitlab.Client, projectID any, opts *gitlab.ListGroupMergeRequestsOptions, assigneeIds []int, reviewerIds []int) ([]*gitlab.BasicMergeRequest, error) {
	if opts.PerPage == 0 {
		opts.PerPage = DefaultListLimit
	}

	mrMap := make(map[int64]*gitlab.BasicMergeRequest)
	for _, id := range assigneeIds {
		opts.AssigneeID = gitlab.AssigneeID(id)
		assigneeMrs, err := listGroupMRsBase(client, projectID, opts)
		if err != nil {
			return nil, err
		}
		for _, mr := range assigneeMrs {
			mrMap[mr.ID] = mr
		}
	}
	opts.AssigneeID = nil // reset because it's Assignee OR Reviewer
	for _, id := range reviewerIds {
		opts.ReviewerID = gitlab.ReviewerID(id)
		reviewerMrs, err := listGroupMRsBase(client, projectID, opts)
		if err != nil {
			return nil, err
		}
		for _, mr := range reviewerMrs {
			mrMap[mr.ID] = mr
		}
	}

	mrs := make([]*gitlab.BasicMergeRequest, 0, len(mrMap))
	for _, mr := range mrMap {
		mrs = append(mrs, mr)
	}

	// Sort by CreatedAt if no custom sort is specified, otherwise let API sorting take precedence
	if opts.OrderBy == nil {
		sort.Slice(mrs, func(i, j int) bool {
			return mrs[i].CreatedAt.After(*mrs[j].CreatedAt)
		})
	}

	return mrs, nil
}

// ListMRs retrieves merge requests for a given project with optional filtering by assignees or reviewers.
//
// Parameters:
//   - client: A GitLab client instance.
//   - projectID: The ID or name of the project.
//   - opts: GitLab-specific options for listing merge requests.
//   - listOpts: Optional list of arguments to filter by assignees or reviewers.
//     May be any combination of api.WithMRAssignees and api.WithMRReviewers.
//
// Returns:
//   - A slice of GitLab merge request objects and an error, if any.
//
// Example usage:
//
//	mrs, err := api.ListMRs(client, "my-group", &gitlab.ListProjectMergeRequestsOptions{},
//		api.WithMRAssignees([]int{123, 456}),
//		api.WithMRReviewers([]int{789}))
//
// Attention: this is a global variable and may be overridden in tests.
var ListMRs = func(client *gitlab.Client, projectID any, opts *gitlab.ListProjectMergeRequestsOptions, listOpts ...CliListMROption) ([]*gitlab.BasicMergeRequest, error) {
	composedListOpts := composeCliListMROptions(listOpts...)
	assigneeIds, reviewerIds := composedListOpts.assigneeIds, composedListOpts.reviewerIds

	if len(assigneeIds) > 0 || len(reviewerIds) > 0 {
		return listMRsWithAssigneesOrReviewers(client, projectID, opts, assigneeIds, reviewerIds)
	} else {
		return listMRsBase(client, projectID, opts)
	}
}

func listMRsBase(client *gitlab.Client, projectID any, opts *gitlab.ListProjectMergeRequestsOptions) ([]*gitlab.BasicMergeRequest, error) {
	if opts.PerPage == 0 {
		opts.PerPage = DefaultListLimit
	}

	mrs, _, err := client.MergeRequests.ListProjectMergeRequests(projectID, opts)
	if err != nil {
		return nil, err
	}
	return mrs, nil
}

func listMRsWithAssigneesOrReviewers(client *gitlab.Client, projectID any, opts *gitlab.ListProjectMergeRequestsOptions, assigneeIds []int, reviewerIds []int) ([]*gitlab.BasicMergeRequest, error) {
	if opts.PerPage == 0 {
		opts.PerPage = DefaultListLimit
	}

	mrMap := make(map[int64]*gitlab.BasicMergeRequest)
	for _, id := range assigneeIds {
		opts.AssigneeID = gitlab.AssigneeID(id)
		assigneeMrs, err := listMRsBase(client, projectID, opts)
		if err != nil {
			return nil, err
		}
		for _, mr := range assigneeMrs {
			mrMap[mr.ID] = mr
		}
	}
	opts.AssigneeID = nil // reset because it's Assignee OR Reviewer
	for _, id := range reviewerIds {
		opts.ReviewerID = gitlab.ReviewerID(id)
		reviewerMrs, err := listMRsBase(client, projectID, opts)
		if err != nil {
			return nil, err
		}
		for _, mr := range reviewerMrs {
			mrMap[mr.ID] = mr
		}
	}

	mrs := make([]*gitlab.BasicMergeRequest, 0, len(mrMap))
	for _, mr := range mrMap {
		mrs = append(mrs, mr)
	}

	// Sort by CreatedAt if no custom sort is specified, otherwise let API sorting take precedence
	if opts.OrderBy == nil {
		sort.Slice(mrs, func(i, j int) bool {
			return mrs[i].CreatedAt.After(*mrs[j].CreatedAt)
		})
	}

	return mrs, nil
}

// UpdateMR updates an MR
// Attention: this is a global variable and may be overridden in tests.
var UpdateMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.UpdateMergeRequestOptions) (*gitlab.MergeRequest, error) {
	mr, _, err := client.MergeRequests.UpdateMergeRequest(projectID, mrID, opts)
	if err != nil {
		return nil, err
	}

	return mr, nil
}

// UpdateMR updates an MR
// Attention: this is a global variable and may be overridden in tests.
var DeleteMR = func(client *gitlab.Client, projectID any, mrID int64) error {
	_, err := client.MergeRequests.DeleteMergeRequest(projectID, mrID)
	if err != nil {
		return err
	}

	return nil
}

type cliListMROptions struct {
	assigneeIds []int
	reviewerIds []int
}

type CliListMROption func(*cliListMROptions)

func WithMRAssignees(assigneeIds []int) CliListMROption {
	return func(c *cliListMROptions) {
		c.assigneeIds = assigneeIds
	}
}

func WithMRReviewers(reviewerIds []int) CliListMROption {
	return func(c *cliListMROptions) {
		c.reviewerIds = reviewerIds
	}
}

func composeCliListMROptions(optionSetters ...CliListMROption) *cliListMROptions {
	opts := &cliListMROptions{}
	for _, setter := range optionSetters {
		setter(opts)
	}
	return opts
}
