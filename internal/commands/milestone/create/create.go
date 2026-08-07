package create

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

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

	projectID string
	groupID   string

	title       string
	description string
	dueDate     string
	startDate   string
}

func NewCmdCreate(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "create [flags]",
		Short: "Create a milestone in a project or group.",
		Long: heredoc.Docf(`
		The %[1]s--title%[1]s flag is required.
		Optionally provide a description, due date, and start date.

		By default, the milestone is created in the current project. Use
		%[1]s--project%[1]s to target a different project, or %[1]s--group%[1]s to create a
		group-level milestone. %[1]s--project%[1]s and %[1]s--group%[1]s are mutually exclusive.
		`, "`"),
		Example: heredoc.Doc(`
			# Create a milestone in the current project
			glab milestone create --title='Example title' --due-date='2025-12-16'

			# Create a milestone in a different project
			glab milestone create --title='Q4 release' --due-date='2025-12-16' --project 123

			# Create a milestone in a group
			glab milestone create --title='FY26 planning' --due-date='2026-01-31' --group 456
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "false",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}

	cmd.Flags().StringVar(&opts.projectID, "project", "", "The ID or URL-encoded path of the project.")
	cmd.Flags().StringVar(&opts.groupID, "group", "", "The ID or URL-encoded path of the group.")

	cmd.Flags().StringVar(&opts.title, "title", "", "Title of the milestone.")
	cmd.Flags().StringVar(&opts.description, "description", "", "Description of the milestone.")
	cmd.Flags().StringVar(&opts.dueDate, "due-date", "", "Due date for the milestone. Expected in ISO 8601 format (2025-04-15T08:00:00Z).")
	cmd.Flags().StringVar(&opts.startDate, "start-date", "", "Start date for the milestone. Expected in ISO 8601 format (2025-04-15T08:00:00Z).")

	cobra.CheckErr(cmd.MarkFlagRequired("title"))

	return cmd
}

func (o *options) run() error {
	c, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := c.Lab()

	var parsedDueDate, parsedStartDate gitlab.ISOTime

	if o.startDate != "" {
		if parsedStartDate, err = gitlab.ParseISOTime(o.startDate); err != nil {
			return err
		}
	}

	if o.dueDate != "" {
		if parsedDueDate, err = gitlab.ParseISOTime(o.dueDate); err != nil {
			return err
		}
	}

	if o.projectID != "" {
		createMilestoneOptions := &gitlab.CreateMilestoneOptions{
			Title:       &o.title,
			Description: &o.description,
		}

		if o.startDate != "" {
			createMilestoneOptions.StartDate = &parsedStartDate
		}

		if o.dueDate != "" {
			createMilestoneOptions.DueDate = &parsedDueDate
		}

		milestone, _, err := client.Milestones.CreateMilestone(o.projectID, createMilestoneOptions)
		if err != nil {
			return err
		}

		o.io.LogInfof("Created project milestone %s (ID: %d)", milestone.Title, milestone.ID)
		return nil
	} else if o.groupID != "" { // get group milestone
		createGroupMilestoneOptions := &gitlab.CreateGroupMilestoneOptions{
			Title:       &o.title,
			Description: &o.description,
		}

		if o.startDate != "" {
			createGroupMilestoneOptions.StartDate = &parsedStartDate
		}

		if o.dueDate != "" {
			createGroupMilestoneOptions.DueDate = &parsedDueDate
		}

		milestone, _, err := client.GroupMilestones.CreateGroupMilestone(o.groupID, createGroupMilestoneOptions)
		if err != nil {
			return err
		}

		o.io.LogInfof("Created group milestone %s (ID: %d)", milestone.Title, milestone.ID)
		return nil
	}

	// run for the current project
	repo, err := o.baseRepo()
	if err != nil {
		return err
	}
	createMilestoneOptions := &gitlab.CreateMilestoneOptions{
		Title:       &o.title,
		Description: &o.description,
	}

	if o.startDate != "" {
		createMilestoneOptions.StartDate = &parsedStartDate
	}
	if o.dueDate != "" {
		createMilestoneOptions.DueDate = &parsedDueDate
	}

	milestone, _, err := client.Milestones.CreateMilestone(repo.FullName(), createMilestoneOptions)
	if err != nil {
		return err
	}

	o.io.LogInfof("Created project milestone %s (ID: %d)", milestone.Title, milestone.ID)
	return nil
}
