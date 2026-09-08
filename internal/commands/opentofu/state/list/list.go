package list

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
)

type options struct {
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: `List states.`,
		Long: heredoc.Doc(`
			Lists the OpenTofu or Terraform states in the current project,
			including each state's latest version serial and lock status.
		`),
		Example: heredoc.Doc(`
			glab opentofu state list
			glab opentofu state list -F json`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	return cmd
}

func (o *options) run() error {
	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	states, _, err := client.TerraformStates.List(repo.FullName())
	if err != nil {
		return err
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(states)
	}

	c := o.io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("Name"), c.Bold("Latest Version Serial"), c.Bold("Created At"), c.Bold("Updated At"), c.Bold("Locked At"))
	for _, state := range states {
		table.AddRow(state.Name, state.LatestVersion.Serial, state.CreatedAt, state.UpdatedAt, state.LockedAt)
	}
	o.io.LogInfof("%s", table.Render())
	return nil
}
