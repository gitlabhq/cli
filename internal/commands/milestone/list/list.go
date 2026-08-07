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
		Use:   "list [flags]",
		Short: "List milestones in a project or group.",
		Long: heredoc.Docf(`
		Filter by state with %[1]s--state%[1]s (%[1]sactive%[1]s or %[1]sclosed%[1]s), by title with %[1]s--title%[1]s,
		or by free-text search with %[1]s--search%[1]s. For group milestones, use
		%[1]s--include-ancestors%[1]s to also include milestones from ancestor groups.

		By default, milestones are listed for the current project. Use
		%[1]s--project%[1]s to target a different project, or %[1]s--group%[1]s to list
		group-level milestones. %[1]s--project%[1]s and %[1]s--group%[1]s are mutually exclusive.

		Use %[1]s--output json%[1]s to format the result as JSON for use with other tools.
		`, "`"),
		Example: heredoc.Doc(`
			# List milestones in a project
			glab milestone list --project 123
			glab milestone list --project owner/project

			# List milestones in a group
			glab milestone list --group example-group

			# List only active milestones in a group
			glab milestone list --group example-group --state active

			# List group milestones, including those from ancestor groups
			glab milestone list --group example-group --include-ancestors

			# List milestones as JSON
			glab milestone list --project owner/project --output json
		`),
		Args: cobra.NoArgs,
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
	cmd.Flags().BoolVar(&opts.includeAncestors, "include-ancestors", false, "Include milestones from all parent groups.")

	cmd.Flags().IntVarP(&opts.page, "page", "p", 1, "Page number.")
	cmd.Flags().IntVarP(&opts.perPage, "per-page", "P", 20, "Number of items to list per page.")
	cmd.Flags().BoolVar(&opts.showIDs, "show-id", false, "Show IDs in table output.")
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

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
