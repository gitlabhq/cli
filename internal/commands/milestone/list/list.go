package list

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams

	// Pagination
	page    int
	perPage int

	title            string
	search           string
	state            string
	includeAncestors bool

	groupID      string
	projectID    string
	showIDs      bool
	outputFormat string
}

func NewCmdList(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Get a list of milestones for a project or group.",
		Long:  "",
		Example: heredoc.Doc(`
		  # List milestones for a given project
			glab milestone list --project 123
			glab milestone list --project example-group/project-path

			# List milestones for a group
			glab milestone list --group example-group

			# List only active milestones for a given group
			glab milestone list --group example-group --state active`),
		Args: cobra.MaximumNArgs(0),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd)
		},
	}

	cmd.Flags().StringVar(&opts.projectID, "project", "", "The ID or URL-encoded path of the project.")
	cmd.Flags().StringVar(&opts.groupID, "group", "", "The ID or URL-encoded path of the group.")

	cmd.Flags().StringVar(&opts.title, "title", "", "Return only the milestones having the given title.")
	cmd.Flags().StringVar(&opts.search, "search", "", "Return only milestones with a title or description matching the provided string.")
	cmd.Flags().StringVar(&opts.state, "state", "", "Return only 'active' or 'closed' milestones.")
	cmd.Flags().BoolVar(&opts.includeAncestors, "include-ancestors", false, "Include milestones from all parent groups. (default false)")

	cmd.Flags().IntVarP(&opts.page, "page", "p", 1, "Page number.")
	cmd.Flags().IntVarP(&opts.perPage, "per-page", "P", 20, "Number of items to list per page.")
	cmd.Flags().BoolVar(&opts.showIDs, "show-id", false, "Show IDs in table output. (default false)")
	cmdutils.EnableJSONOutput(cmd, &opts.outputFormat)

	cmd.MarkFlagsOneRequired("project", "group")

	return cmd
}

func (o *options) run(cmd *cobra.Command) error {
	c, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := c.Lab()
	table := tableprinter.NewTablePrinter()
	if o.showIDs {
		table.AddRow("ID", "Title", "Description", "State", "Due Date")
	} else {
		table.AddRow("Title", "Description", "State", "Due Date")
	}

	if o.projectID != "" { // list project milestones
		listMilestonesOptions := &gitlab.ListMilestonesOptions{
			ListOptions: gitlab.ListOptions{
				PerPage: int64(o.perPage),
				Page:    int64(o.page),
			},
		}

		if o.title != "" {
			listMilestonesOptions.Title = &o.title
		}
		if o.search != "" {
			listMilestonesOptions.Search = &o.search
		}
		if o.state != "" {
			listMilestonesOptions.State = &o.state
		}
		if cmd.Flags().Changed("include-ancestors") {
			listMilestonesOptions.IncludeAncestors = &o.includeAncestors
		}

		milestones, _, err := client.Milestones.ListMilestones(o.projectID, listMilestonesOptions)
		if err != nil {
			return err
		}

		if o.outputFormat == "json" {
			return o.io.PrintJSON(milestones)
		}

		if len(milestones) == 0 {
			o.io.LogInfo("No milestones found.")
			return nil
		}

		if o.showIDs {
			for _, m := range milestones {
				table.AddRow(m.ID, m.Title, m.Description, m.State, utils.FormatDueDate(m.DueDate))
			}
		} else {
			for _, m := range milestones {
				table.AddRow(m.Title, m.Description, m.State, utils.FormatDueDate(m.DueDate))
			}
		}

		o.io.LogInfo(table.String())
		return nil
	} else if o.groupID != "" { // list group milestones
		listMilestonesOptions := &gitlab.ListGroupMilestonesOptions{
			ListOptions: gitlab.ListOptions{
				Page:    int64(o.page),
				PerPage: int64(o.perPage),
			},
		}

		if o.title != "" {
			listMilestonesOptions.Title = &o.title
		}
		if o.search != "" {
			listMilestonesOptions.Search = &o.search
		}
		if o.state != "" {
			listMilestonesOptions.State = &o.state
		}
		if cmd.Flags().Changed("include-ancestors") {
			listMilestonesOptions.IncludeAncestors = &o.includeAncestors
		}

		milestones, _, err := client.GroupMilestones.ListGroupMilestones(o.groupID, listMilestonesOptions)
		if err != nil {
			return err
		}

		if o.outputFormat == "json" {
			return o.io.PrintJSON(milestones)
		}

		if len(milestones) == 0 {
			o.io.LogInfo("No milestones found.")
			return nil
		}

		if o.showIDs {
			for _, m := range milestones {
				table.AddRow(m.ID, m.Title, m.Description, m.State, utils.FormatDueDate(m.DueDate))
			}
		} else {
			for _, m := range milestones {
				table.AddRow(m.Title, m.Description, m.State, utils.FormatDueDate(m.DueDate))
			}
		}

		o.io.LogInfo(table.String())
		return nil
	}

	return nil
}
