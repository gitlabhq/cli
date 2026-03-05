package list

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
)

type options struct {
	orderBy          string
	sort             string
	group            string
	includeSubgroups bool
	perPage          int
	page             int
	outputFormat     string
	filterAll        bool
	filterOwner      bool
	filterMember     bool
	filterStarred    bool
	archived         bool
	archivedSet      bool
	user             string

	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
}

func NewCmdList(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}
	repoListCmd := &cobra.Command{
		Use:   "list",
		Short: `Get list of repositories.`,
		Example: heredoc.Doc(`
			$ glab repo list
		`),
		Args:    cobra.ExactArgs(0),
		Aliases: []string{"ls"},
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(cmd)

			return opts.run()
		},
	}

	repoListCmd.Flags().StringVarP(&opts.orderBy, "order", "o", "last_activity_at", "Return repositories ordered by id, name, path, created_at, updated_at, similarity, star_count, last_activity_at.")
	repoListCmd.Flags().StringVarP(&opts.sort, "sort", "s", "", "Sort direction for --order field: asc or desc.")
	repoListCmd.Flags().StringVarP(&opts.group, "group", "g", "", "Return repositories in only the given group.")
	repoListCmd.Flags().BoolVarP(&opts.includeSubgroups, "include-subgroups", "G", false, "Include projects in subgroups of this group. Default is false. Used with the '--group' flag.")
	repoListCmd.Flags().IntVarP(&opts.page, "page", "p", 1, "Page number.")
	repoListCmd.Flags().IntVarP(&opts.perPage, "per-page", "P", 30, "Number of items to list per page.")
	cmdutils.EnableJSONOutput(repoListCmd, &opts.outputFormat)
	repoListCmd.Flags().BoolVarP(&opts.filterAll, "all", "a", false, "List all projects on the instance.")
	repoListCmd.Flags().BoolVarP(&opts.filterOwner, "mine", "m", false, "List only projects you own. Default if no filters are provided.")
	repoListCmd.Flags().StringVarP(&opts.user, "user", "u", "", "List user projects.")
	repoListCmd.Flags().BoolVar(&opts.filterMember, "member", false, "List only projects of which you are a member.")
	repoListCmd.Flags().BoolVar(&opts.filterStarred, "starred", false, "List only starred projects.")
	repoListCmd.Flags().BoolVar(&opts.archived, "archived", false, "Limit by archived status. Use 'false' to exclude archived repositories. Used with the '--group' flag.")

	repoListCmd.MarkFlagsMutuallyExclusive("user", "group")
	return repoListCmd
}

func (o *options) complete(cmd *cobra.Command) {
	o.archivedSet = cmd.Flags().Changed("archived")
}

func (o *options) run() error {
	var err error
	c := o.io.Color()

	apiClient, err := o.apiClient("")
	if err != nil {
		return err
	}
	gitlabClient := apiClient.Lab()

	var projects []*gitlab.Project
	var resp *gitlab.Response
	if len(o.group) > 0 {
		projects, resp, err = listAllProjectsForGroup(gitlabClient, *o)
	} else if o.user != "" {
		projects, resp, err = listAllProjectsForUser(gitlabClient, *o)
	} else {
		projects, resp, err = listAllProjects(gitlabClient, *o)
	}

	if err != nil {
		return err
	}

	if o.outputFormat == "json" {
		projectListJSON, _ := json.Marshal(projects)
		fmt.Fprintln(o.io.StdOut, string(projectListJSON))
	} else {
		// Title
		title := fmt.Sprintf("Showing %d of %d projects (Page %d of %d).\n", len(projects), resp.TotalItems, resp.CurrentPage, resp.TotalPages)

		// List
		table := tableprinter.NewTablePrinter()
		if len(projects) > 0 {
			table.AddRow("Project path", "Git URL", "Description")
		}

		for _, prj := range projects {
			table.AddCell(c.Blue(prj.PathWithNamespace))
			table.AddCell(prj.SSHURLToRepo)
			table.AddCell(prj.Description)
			table.EndRow()
		}

		fmt.Fprintf(o.io.StdOut, "%s\n%s\n", title, table.String())
	}

	return err
}

func listAllProjects(apiClient *gitlab.Client, opts options) ([]*gitlab.Project, *gitlab.Response, error) {
	l := &gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: int64(opts.perPage),
			Page:    int64(opts.page),
		},
		OrderBy: gitlab.Ptr(opts.orderBy),
	}

	// Other filters only valid if FilterAll not true
	if !opts.filterAll {
		if !opts.filterStarred && !opts.filterMember {
			// if no other filters are passed, default to Owned filter
			l.Owned = gitlab.Ptr(true)
		}

		if opts.filterOwner {
			l.Owned = gitlab.Ptr(opts.filterOwner)
		}

		if opts.filterStarred {
			l.Starred = gitlab.Ptr(opts.filterStarred)
		}

		if opts.filterMember {
			l.Membership = gitlab.Ptr(opts.filterMember)
		}
	}

	if opts.archivedSet {
		l.Archived = gitlab.Ptr(opts.archived)
	}

	if opts.sort != "" {
		l.Sort = gitlab.Ptr(opts.sort)
	}

	return apiClient.Projects.ListProjects(l)
}

func listAllProjectsForGroup(apiClient *gitlab.Client, opts options) ([]*gitlab.Project, *gitlab.Response, error) {
	group, resp, err := apiClient.Groups.GetGroup(opts.group, &gitlab.GetGroupOptions{})
	if err != nil {
		if errors.Is(err, gitlab.ErrNotFound) {
			return nil, nil, fmt.Errorf("No group matching path %s", opts.group)
		}
		return nil, resp, err
	}

	l := &gitlab.ListGroupProjectsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: int64(opts.perPage),
			Page:    int64(opts.page),
		},
		OrderBy: gitlab.Ptr(opts.orderBy),
	}

	// Other filters only valid if FilterAll not true
	if !opts.filterAll {
		if !opts.filterStarred && !opts.filterMember {
			// if no other filters are passed, default to Owned filter
			l.Owned = gitlab.Ptr(true)
		}

		if opts.filterOwner {
			l.Owned = gitlab.Ptr(opts.filterOwner)
		}

		if opts.filterStarred {
			l.Starred = gitlab.Ptr(opts.filterStarred)
		}
	}

	if opts.includeSubgroups {
		l.IncludeSubGroups = gitlab.Ptr(true)
	}

	if opts.archivedSet {
		l.Archived = gitlab.Ptr(opts.archived)
	}

	if opts.sort != "" {
		l.Sort = gitlab.Ptr(opts.sort)
	}

	return apiClient.Groups.ListGroupProjects(group.ID, l)
}

func listAllProjectsForUser(apiClient *gitlab.Client, opts options) ([]*gitlab.Project, *gitlab.Response, error) {
	l := &gitlab.ListProjectsOptions{
		OrderBy: gitlab.Ptr(opts.orderBy),
		ListOptions: gitlab.ListOptions{
			PerPage: int64(opts.perPage),
			Page:    int64(opts.page),
		},
	}

	if opts.archivedSet {
		l.Archived = gitlab.Ptr(opts.archived)
	}

	if opts.filterStarred {
		l.Starred = gitlab.Ptr(opts.filterStarred)
	}

	if opts.sort != "" {
		l.Sort = gitlab.Ptr(opts.sort)
	}

	return apiClient.Projects.ListUserProjects(opts.user, l)
}
