package list

import (
	"context"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
	baseRepo  func() (glrepo.Interface, error)

	page         int64
	perPage      int64
	outputFormat string
	group        string
	instance     bool
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: "List runners.",
		Long: heredoc.Doc(`
			List runners for a project (default), group, or instance.

			Instance scope requires administrator access.
		`),
		Aliases: []string{"ls"},
		Args:    cobra.NoArgs,
		Example: heredoc.Doc(`
			# List runners for the current project
			glab runner list

			# List runners for a specific project
			glab runner list -R owner/repo

			# List runners for a group
			glab runner list --group mygroup

			# List runners as JSON
			glab runner list --output json`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)
	fl := cmd.Flags()
	fl.Int64VarP(&opts.page, "page", "p", 1, "Page number.")
	fl.Int64VarP(&opts.perPage, "per-page", "P", api.DefaultListLimit, "Number of items to list per page.")
	fl.StringVarP(&opts.group, "group", "g", "", "List runners for a group. Ignored if -R/--repo is set.")
	fl.BoolVarP(&opts.instance, "instance", "i", false, "List all runners available to the user (instance scope).")
	cmd.MarkFlagsMutuallyExclusive("instance", "group", "repo")

	return cmd
}

func (o *options) complete(cmd *cobra.Command) error {
	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	o.group = group
	return nil
}

func (o *options) run(ctx context.Context) error {
	repo, repoErr := o.baseRepo()
	var repoHost string
	if repoErr == nil {
		repoHost = repo.RepoHost()
	}
	apiClient, err := o.apiClient(repoHost)
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	listOpts := &gitlab.ListRunnersOptions{
		ListOptions: gitlab.ListOptions{
			Page:    o.page,
			PerPage: o.perPage,
		},
	}

	var runners []*gitlab.Runner
	var scopeLabel string

	listOptsBase := gitlab.ListOptions{
		Page:    o.page,
		PerPage: o.perPage,
	}

	switch {
	case o.instance:
		runners, _, err = client.Runners.ListRunners(listOpts, gitlab.WithContext(ctx))
		if err != nil {
			return err
		}
		scopeLabel = "instance"
	case o.group != "":
		runners, _, err = client.Runners.ListGroupsRunners(o.group, &gitlab.ListGroupsRunnersOptions{
			ListOptions: listOptsBase,
		}, gitlab.WithContext(ctx))
		if err != nil {
			return err
		}
		scopeLabel = o.group
	default:
		if repoErr != nil {
			return repoErr
		}
		runners, _, err = client.Runners.ListProjectRunners(repo.FullName(), &gitlab.ListProjectRunnersOptions{
			ListOptions: listOptsBase,
		}, gitlab.WithContext(ctx))
		if err != nil {
			return err
		}
		scopeLabel = repo.FullName()
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(runners)
	default:
		return o.printTable(runners, scopeLabel)
	}
}

func (o *options) printTable(runners []*gitlab.Runner, scopeLabel string) error {
	title := utils.NewListTitle("runner")
	title.RepoName = scopeLabel
	title.Page = int(o.page)
	title.CurrentPageTotal = len(runners)

	if err := o.io.StartPager(); err != nil {
		return err
	}
	defer o.io.StopPager()

	o.io.LogInfof("%s\n%s\n", title.Describe(), displayRunners(o.io, runners))
	return nil
}

func displayRunners(io *iostreams.IOStreams, runners []*gitlab.Runner) string {
	c := io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("ID"), c.Bold("Description"), c.Bold("Status"), c.Bold("Paused"))
	for _, r := range runners {
		table.AddRow(
			r.ID,
			r.Description,
			formatStatus(c, r.Status),
			r.Paused,
		)
	}
	return table.Render()
}

func formatStatus(c *iostreams.ColorPalette, status string) string {
	switch strings.ToLower(status) {
	case "online":
		return c.Green(status)
	case "offline":
		return c.Gray(status)
	case "stale":
		return c.Magenta(status)
	case "never_contacted":
		return c.Yellow(status)
	case "paused":
		return c.Yellow(status)
	default:
		return status
	}
}
