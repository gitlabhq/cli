package list

import (
	"context"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/service-account/serviceaccountutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
	baseRepo  func() (glrepo.Interface, error)

	group        string
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:               "list [flags]",
		Short:             "List service accounts.",
		Aliases:           []string{"ls"},
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Doc(`
			List the service accounts of a project or a group.
		`),
		Example: heredoc.Doc(`
			# List the current project's service accounts
			glab service-account list

			# List the service accounts of a specific project
			glab service-account list --repo user/my-repo

			# List a group's service accounts
			glab service-account list --group my-group

			# Print the ID and username of each service account
			glab service-account list --output json --jq '.[] | "\(.id) \(.username)"'`),
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

	fl := cmd.Flags()
	fl.StringVarP(&opts.group, "group", "g", "", "List the service accounts of a `group` instead of a project. Accepts a group ID or full path.")

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)
	cmd.MarkFlagsMutuallyExclusive("repo", "group")

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
	client, scope, err := serviceaccountutils.NewClient(o.baseRepo, o.apiClient, o.group)
	if err != nil {
		return err
	}

	raw, accounts, err := serviceaccountutils.List(ctx, client, scope)
	if err != nil {
		return cmdutils.WrapError(err, "failed to list service accounts")
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(raw)
	default:
		return o.printTable(accounts)
	}
}

func (o *options) printTable(accounts []serviceaccountutils.Account) error {
	if len(accounts) == 0 {
		o.io.LogInfo("No service accounts found.")
		return nil
	}

	c := o.io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("ID"), c.Bold("Username"), c.Bold("Name"), c.Bold("Email"))
	for _, a := range accounts {
		table.AddRow(a.ID, a.Username, serviceaccountutils.OrDash(a.Name), serviceaccountutils.OrDash(a.Email))
	}
	o.io.LogInfof("%s", table.Render())

	return nil
}
