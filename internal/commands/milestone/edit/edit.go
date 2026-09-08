package edit

import (
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams
	baseRepo  func() (glrepo.Interface, error)

	projectID   string
	groupID     string
	milestoneID int64

	title       string
	description string
	dueDate     string
	startDate   string
	state       string
}

func NewCmdEdit(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "edit <id> [flags]",
		Short: "Edit a milestone in a project or group.",
		Long: heredoc.Docf(`
		Edit a milestone, identified by its numeric ID, in a project or group.
		Use the flags to update the title, description, due date, start date,
		or state. Only the fields you specify are updated.

		By default, the milestone is edited in the current project. Use
		%[1]s--project%[1]s to target a different project, or %[1]s--group%[1]s to edit a
		group-level milestone. %[1]s--project%[1]s and %[1]s--group%[1]s are mutually exclusive.
		`, "`"),
		Example: heredoc.Doc(`
			# Update a milestone's title and due date in the current project
			glab milestone edit 123 --title='Example title' --due-date='2025-12-16'

			# Update a milestone in a different project
			glab milestone edit 123 --title='Q4 release' --due-date='2025-12-16' --project owner/project

			# Update a group milestone
			glab milestone edit 123 --title='FY26 planning' --due-date='2026-01-31' --group 789
		`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "false",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			milestoneIDInt, err := strconv.Atoi(args[0])
			if err != nil {
				return err
			}
			opts.milestoneID = int64(milestoneIDInt)
			return opts.run()
		},
	}

	cmd.Flags().StringVar(&opts.projectID, "project", "", "The ID or URL-encoded path of the project.")
	cmd.Flags().StringVar(&opts.groupID, "group", "", "The ID or URL-encoded path of the group.")

	cmd.Flags().StringVar(&opts.title, "title", "", "Title of the milestone.")
	cmd.Flags().StringVar(&opts.description, "description", "", "Description of the milestone.")
	cmd.Flags().StringVar(&opts.dueDate, "due-date", "", "Due date for the milestone. Expected in ISO 8601 format (2025-04-15T08:00:00Z).")
	cmd.Flags().StringVar(&opts.startDate, "start-date", "", "Start date for the milestone. Expected in ISO 8601 format (2025-04-15T08:00:00Z).")
	cmd.Flags().StringVar(&opts.state, "state", "", "State of the milestone. Can be 'activate' or 'close'.")

	return cmd
}

func (o *options) run() error {
	c, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := c.Lab()

	updateMilestoneOptions, updateGroupMilestoneOptions, err := createOptions(o)
	if err != nil {
		return err
	}

	switch {
	case o.projectID != "":
		milestone, _, err := client.Milestones.UpdateMilestone(o.projectID, o.milestoneID, updateMilestoneOptions)
		if err != nil {
			return err
		}
		o.io.LogInfof("Updated project milestone %s (ID: %d)", milestone.Title, milestone.ID)
	case o.groupID != "":
		milestone, _, err := client.GroupMilestones.UpdateGroupMilestone(o.groupID, o.milestoneID, updateGroupMilestoneOptions)
		if err != nil {
			return err
		}
		o.io.LogInfof("Updated group milestone %s (ID: %d)", milestone.Title, milestone.ID)
	default:
		repo, err := o.baseRepo()
		if err != nil {
			return err
		}
		milestone, _, err := client.Milestones.UpdateMilestone(repo.FullName(), o.milestoneID, updateMilestoneOptions)
		if err != nil {
			return err
		}
		o.io.LogInfof("Updated project milestone %s (ID: %d)", milestone.Title, milestone.ID)

	}
	return nil
}

// createOptions - helper function used to create the UpdateMilestoneOptions and UpdateGroupMilestoneOptions
func createOptions(o *options) (*gitlab.UpdateMilestoneOptions, *gitlab.UpdateGroupMilestoneOptions, error) {
	var parsedDueDate, parsedStartDate gitlab.ISOTime
	var err error

	if o.startDate != "" {
		if parsedStartDate, err = gitlab.ParseISOTime(o.startDate); err != nil {
			return nil, nil, err
		}
	}

	if o.dueDate != "" {
		if parsedDueDate, err = gitlab.ParseISOTime(o.dueDate); err != nil {
			return nil, nil, err
		}
	}

	updateMilestoneOptions := &gitlab.UpdateMilestoneOptions{}
	updateGroupMilestoneOptions := &gitlab.UpdateGroupMilestoneOptions{}

	if o.title != "" {
		updateMilestoneOptions.Title = &o.title
		updateGroupMilestoneOptions.Title = &o.title
	}
	if o.description != "" {
		updateMilestoneOptions.Description = &o.description
		updateGroupMilestoneOptions.Description = &o.description
	}

	if o.startDate != "" {
		updateMilestoneOptions.StartDate = &parsedStartDate
		updateGroupMilestoneOptions.StartDate = &parsedStartDate
	}

	if o.dueDate != "" {
		updateMilestoneOptions.DueDate = &parsedDueDate
		updateGroupMilestoneOptions.DueDate = &parsedDueDate
	}

	if o.state != "" {
		updateMilestoneOptions.StateEvent = &o.state
		updateGroupMilestoneOptions.StateEvent = &o.state
	}

	return updateMilestoneOptions, updateGroupMilestoneOptions, nil
}
