package managers

import (
	"context"
	"strconv"
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
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)
	apiClient    func(repoHost string) (*api.Client, error)
	runnerID     int64
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		baseRepo:  f.BaseRepo,
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "managers <runner-id> [flags]",
		Short: "List runner managers.",
		Long: heredoc.Doc(`
			Lists the managers of a runner.
		`),
		Example: heredoc.Doc(`
			# List managers for runner 1
			glab runner managers 1

			# List managers as JSON
			glab runner managers 1 --output json`),
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
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)
	cmdutils.EnableRepoOverride(cmd, f)
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

	managers, _, err := client.Runners.ListRunnerManagers(o.runnerID, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, "failed to list runner managers")
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(managers)
	default:
		return o.printTable(managers)
	}
}

func (o *options) printTable(managers []*gitlab.RunnerManager) error {
	title := utils.NewListTitle("manager")
	title.CurrentPageTotal = len(managers)

	if err := o.io.StartPager(); err != nil {
		return err
	}
	defer o.io.StopPager()

	o.io.LogInfof("%s\n%s\n", title.Describe(), displayManagers(o.io, managers))
	return nil
}

func displayManagers(io *iostreams.IOStreams, managers []*gitlab.RunnerManager) string {
	c := io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(
		c.Bold("ID"),
		c.Bold("System ID"),
		c.Bold("Version"),
		c.Bold("Platform"),
		c.Bold("Architecture"),
		c.Bold("IP Address"),
		c.Bold("Status"),
	)
	for _, m := range managers {
		table.AddRow(
			m.ID,
			m.SystemID,
			m.Version,
			m.Platform,
			m.Architecture,
			m.IPAddress,
			formatManagerStatus(c, m.Status),
		)
	}
	return table.Render()
}

func formatManagerStatus(c *iostreams.ColorPalette, status string) string {
	switch strings.ToLower(status) {
	case "online":
		return c.Green(status)
	case "offline":
		return c.Gray(status)
	case "stale":
		return c.Magenta(status)
	case "never_contacted":
		return c.Yellow(status)
	default:
		return status
	}
}
