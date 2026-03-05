package search

import (
	"encoding/json"
	"fmt"

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
	perPage      int
	page         int
	search       string
	outputFormat string
	apiClient    func(repoHost string) (*api.Client, error)
	io           *iostreams.IOStreams
}

func NewCmdSearch(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	projectSearchCmd := &cobra.Command{
		Use:     "search [flags]",
		Short:   `Search for GitLab repositories and projects by name.`,
		Long:    ``,
		Args:    cobra.ExactArgs(0),
		Aliases: []string{"find", "lookup"},
		Example: heredoc.Doc(`
			$ glab project search -s "title"
			$ glab repo search -s "title"
			$ glab project find -s "title"
			$ glab project lookup -s "title"
		`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}

	projectSearchCmd.Flags().IntVarP(&opts.page, "page", "p", 1, "Page number.")
	projectSearchCmd.Flags().IntVarP(&opts.perPage, "per-page", "P", 20, "Number of items to list per page.")
	projectSearchCmd.Flags().StringVarP(&opts.search, "search", "s", "", "A string contained in the project name.")
	cmdutils.EnableJSONOutput(projectSearchCmd, &opts.outputFormat)
	cobra.CheckErr(projectSearchCmd.MarkFlagRequired("search"))

	return projectSearchCmd
}

func (o *options) run() error {
	c, err := o.apiClient("")
	if err != nil {
		return err
	}
	gitlabClient := c.Lab()
	listOpts := gitlab.ListOptions{
		Page:    int64(o.page),
		PerPage: int64(o.perPage),
	}
	projects, _, err := gitlabClient.Search.Projects(o.search, &gitlab.SearchOptions{ListOptions: listOpts})
	if err != nil {
		return err
	}
	if o.outputFormat == "json" {
		projectListJSON, err := json.Marshal(projects)
		if err != nil {
			return err
		}
		fmt.Fprintln(o.io.StdOut, string(projectListJSON))
		return nil
	}
	title := fmt.Sprintf("Showing results for \"%s\"\n", o.search)
	if len(projects) == 0 {
		title = fmt.Sprintf("No results found for \"%s\"", o.search)
	}

	table := tableprinter.NewTablePrinter()
	if len(projects) > 0 {
		table.AddRow("Project ID", "Project path", "Description", "Stars, forks, open issues", "Updated at")
	}
	table.Wrap = false
	for _, p := range projects {
		description := ""
		if p.Description != "" {
			description = o.io.Color().Cyan(p.Description)
		}

		metadata := fmt.Sprintf("%d stars %d forks %d issues", p.StarCount, p.ForksCount, p.OpenIssuesCount)

		table.AddCell(o.io.Color().Green(fmt.Sprintf("%d", p.ID)))
		table.AddCell(p.PathWithNamespace)
		table.AddCell(description)
		table.AddCell(metadata)
		table.AddCell(utils.TimeToPrettyTimeAgo(*p.LastActivityAt))
		table.EndRow()
	}

	fmt.Fprintf(o.io.StdOut, "%s\n%s\n", title, table.Render())
	return nil
}
