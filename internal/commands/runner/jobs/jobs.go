package jobs

import (
	"context"
	"strconv"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)
	apiClient    func(repoHost string) (*api.Client, error)
	runnerID     int64
	status       string
	orderBy      string
	sort         string
	page         int64
	perPage      int64
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		baseRepo:  f.BaseRepo,
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "jobs <runner-id> [flags]",
		Short: "List jobs processed by a runner.",
		Long: heredoc.Doc(`
			Lists jobs processed by the specified runner, including jobs that are currently running.

			Requires the Maintainer or Owner role for the project.
		`),
		Example: heredoc.Doc(`
			# List all jobs for runner 9
			glab runner jobs 9

			# List only running jobs
			glab runner jobs 9 --status running

			# List jobs as JSON
			glab runner jobs 9 --output json`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&opts.status, "status", "", "Filter jobs by status: running, success, failed, canceled.")
	fl.StringVar(&opts.orderBy, "order-by", "id", "Order jobs by: id.")
	fl.StringVar(&opts.sort, "sort", "desc", "Sort order: asc or desc.")
	fl.Int64VarP(&opts.page, "page", "p", 1, "Page number.")
	fl.Int64VarP(&opts.perPage, "per-page", "P", api.DefaultListLimit, "Number of items to list per page.")

	cmdutils.EnableRepoOverride(cmd, f)
	cmdutils.EnableJSONOutput(cmd, &opts.outputFormat)

	return cmd
}

func (o *options) complete(args []string) error {
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return cmdutils.WrapError(err, "invalid runner ID")
	}
	o.runnerID = id
	return nil
}

func (o *options) run(ctx context.Context) error {
	var repoHost string
	if repo, err := o.baseRepo(); err == nil {
		repoHost = repo.RepoHost()
	}

	apiClient, err := o.apiClient(repoHost)
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	listOpts := &gitlab.ListRunnerJobsOptions{
		ListOptions: gitlab.ListOptions{
			Page:    o.page,
			PerPage: o.perPage,
		},
	}
	if o.status != "" {
		listOpts.Status = new(strings.ToLower(o.status))
	}
	if o.orderBy != "" {
		listOpts.OrderBy = new(o.orderBy)
	}
	if o.sort != "" {
		listOpts.Sort = new(strings.ToLower(o.sort))
	}

	jobs, _, err := client.Runners.ListRunnerJobs(o.runnerID, listOpts, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, "failed to list runner jobs")
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(jobs)
	default:
		return o.printTable(jobs)
	}
}

func (o *options) printTable(jobs []*gitlab.Job) error {
	title := utils.NewListTitle("job")
	title.Page = int(o.page)
	title.CurrentPageTotal = len(jobs)

	if err := o.io.StartPager(); err != nil {
		return err
	}
	defer o.io.StopPager()

	o.io.LogInfof("%s\n%s\n", title.Describe(), displayJobs(o.io, jobs))
	return nil
}

func displayJobs(io *iostreams.IOStreams, jobs []*gitlab.Job) string {
	c := io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("ID"), c.Bold("Status"), c.Bold("Stage"), c.Bold("Name"), c.Bold("Ref"), c.Bold("Project"))
	for _, j := range jobs {
		projectPath := ""
		if j.Project != nil {
			projectPath = j.Project.PathWithNamespace
		}
		table.AddRow(
			j.ID,
			formatJobStatus(c, j.Status),
			j.Stage,
			j.Name,
			j.Ref,
			projectPath,
		)
	}
	return table.Render()
}

func formatJobStatus(c *iostreams.ColorPalette, status string) string {
	switch strings.ToLower(status) {
	case "success":
		return c.Green(status)
	case "failed", "canceled":
		return c.Red(status)
	case "running":
		return c.Blue(status)
	default:
		return status
	}
}
