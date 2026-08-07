package contributors

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	orderBy      string
	sort         string
	perPage      int
	page         int
	outputFormat string

	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)
	io           *iostreams.IOStreams
}

func NewCmdContributors(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
	}
	repoContributorsCmd := &cobra.Command{
		Use:   "contributors",
		Short: `Get repository contributors list.`,
		Long: heredoc.Docf(`
			Lists the users who have contributed commits to the repository, with
			each contributor's commit count. Use %[1]s--repo%[1]s to target a different
			repository.
		`, "`"),
		Example: heredoc.Doc(`
			# List contributors for the current repository
			glab repo contributors

			# List contributors for a specific repository
			glab repo contributors -R gitlab-com/www-gitlab-com`),
		Args:    cobra.NoArgs,
		Aliases: []string{"users"},
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}

	cmdutils.EnableRepoOverride(repoContributorsCmd, f)

	repoContributorsCmd.Flags().StringVarP(&opts.orderBy, "order", "o", "commits", "Return contributors ordered by name, email, or commits (orders by commit date) fields.")
	repoContributorsCmd.Flags().StringVarP(&opts.sort, "sort", "s", "", "Sort direction for --order field: asc or desc.")
	repoContributorsCmd.Flags().IntVarP(&opts.page, "page", "p", 1, "Page number.")
	repoContributorsCmd.Flags().IntVarP(&opts.perPage, "per-page", "P", 30, "Number of items to list per page.")
	cmdutils.EnableJSONOutput(repoContributorsCmd, opts.io, &opts.outputFormat)
	return repoContributorsCmd
}

func (o *options) run() error {
	var err error
	c := o.io.Color()

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	if o.orderBy == "commits" && o.sort == "" {
		o.sort = "desc"
	}

	l := &gitlab.ListContributorsOptions{
		OrderBy: new(o.orderBy),
		ListOptions: gitlab.ListOptions{
			Page:    int64(o.page),
			PerPage: int64(o.perPage),
		},
	}

	if o.sort != "" {
		l.Sort = new(o.sort)
	}

	users, _, err := client.Repositories.Contributors(repo.FullName(), l)
	if err != nil {
		return err
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(users)
	}

	// Title
	title := utils.NewListTitle("contributor")
	title.RepoName = repo.FullName()
	title.Page = int(l.Page)
	title.CurrentPageTotal = len(users)

	// List
	table := tableprinter.NewTablePrinter()
	for _, user := range users {
		table.AddCell(user.Name)
		table.AddCellf("%s", c.Gray(user.Email))
		table.AddCellf("%d commits", user.Commits)
		table.EndRow()
	}

	o.io.LogInfof("%s\n%s\n", title.Describe(), table.String())
	return err
}
