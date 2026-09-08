package api

import (
	"fmt"

	"golang.org/x/sync/errgroup"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

// Describe namespace kinds which is either group or user
// See docs: https://docs.gitlab.com/api/namespaces/
const (
	NamespaceKindUser  = "user"
	NamespaceKindGroup = "group"
)

type Milestone struct {
	ID    int64
	Title string
}

func NewProjectMilestone(m *gitlab.Milestone) *Milestone {
	return &Milestone{
		ID:    m.ID,
		Title: m.Title,
	}
}

func NewGroupMilestone(m *gitlab.GroupMilestone) *Milestone {
	return &Milestone{
		ID:    m.ID,
		Title: m.Title,
	}
}

type ListMilestonesOptions struct {
	IIDs                    []int64
	State                   *string
	Title                   *string
	Search                  *string
	IncludeParentMilestones *bool
	PerPage                 int64
	Page                    int64
}

func (opts *ListMilestonesOptions) ListProjectMilestonesOptions() *gitlab.ListMilestonesOptions {
	projectOpts := &gitlab.ListMilestonesOptions{
		IIDs:   &opts.IIDs,
		State:  opts.State,
		Title:  opts.Title,
		Search: opts.Search,
	}
	projectOpts.PerPage = opts.PerPage
	projectOpts.Page = opts.Page
	return projectOpts
}

func (opts *ListMilestonesOptions) ListGroupMilestonesOptions() *gitlab.ListGroupMilestonesOptions {
	groupOpts := &gitlab.ListGroupMilestonesOptions{
		IIDs:                    &opts.IIDs,
		State:                   opts.State,
		Title:                   opts.Title,
		Search:                  opts.Search,
		IncludeParentMilestones: opts.IncludeParentMilestones,
	}
	groupOpts.PerPage = opts.PerPage
	groupOpts.Page = opts.Page
	return groupOpts
}

func listGroupMilestones(client *gitlab.Client, groupID any, opts *gitlab.ListGroupMilestonesOptions) ([]*gitlab.GroupMilestone, error) {
	if opts.PerPage == 0 {
		opts.PerPage = DefaultListLimit
	}

	milestone, _, err := client.GroupMilestones.ListGroupMilestones(groupID, opts)
	if err != nil {
		return nil, err
	}
	return milestone, nil
}

func listProjectMilestones(client *gitlab.Client, projectID any, opts *gitlab.ListMilestonesOptions) ([]*gitlab.Milestone, error) {
	if opts.PerPage == 0 {
		opts.PerPage = DefaultListLimit
	}

	milestone, _, err := client.Milestones.ListMilestones(projectID, opts)
	if err != nil {
		return nil, err
	}
	return milestone, nil
}

var ListAllMilestones = func(client *gitlab.Client, projectID any, opts *ListMilestonesOptions) ([]*Milestone, error) {
	project, err := GetProject(client, projectID)
	if err != nil {
		return nil, err
	}

	errGroup := &errgroup.Group{}
	projectMilestones := []*gitlab.Milestone{}
	groupMilestones := []*gitlab.GroupMilestone{}

	errGroup.Go(func() error {
		var err error
		projectMilestones, err = listProjectMilestones(client, projectID, opts.ListProjectMilestonesOptions())
		return err
	})

	if project.Namespace.Kind == NamespaceKindGroup {
		errGroup.Go(func() error {
			var err error
			groupMilestones, err = listGroupMilestones(client, project.Namespace.ID, opts.ListGroupMilestonesOptions())
			return err
		})
	}

	if err := errGroup.Wait(); err != nil {
		return nil, fmt.Errorf("failed to get all project related milestones. %w", err)
	}

	milestones := make([]*Milestone, 0, len(projectMilestones)+len(groupMilestones))
	for _, v := range projectMilestones {
		milestones = append(milestones, NewProjectMilestone(v))
	}

	for _, v := range groupMilestones {
		milestones = append(milestones, NewGroupMilestone(v))
	}

	return milestones, nil
}
