package get

import (
	"context"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

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

	account      string
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
		Use:               "get <id | username> [flags]",
		Short:             "Get a service account.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Docf(`
			Get a single service account of a project or a group, by its ID or username.

			A numeric argument is always treated as an ID. IDs and usernames come from %[1]sglab service-account list%[1]s.

			Getting a service account by ID requires GitLab 19.4 or later.
		`, "`"),
		Example: heredoc.Doc(`
			# Get service account 42 of the current project
			glab service-account get 42

			# Get a service account by its username
			glab service-account get service_account_project_123_abc

			# Get a service account of a specific project
			glab service-account get 42 --repo user/my-repo

			# Get a group's service account
			glab service-account get 42 --group my-group

			# Print the service account's email address
			glab service-account get 42 --output json --jq '.email'`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)

	fl := cmd.Flags()
	fl.StringVarP(&opts.group, "group", "g", "", "Get a service account of a `group` instead of a project. Accepts a group ID or full path.")

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)
	cmd.MarkFlagsMutuallyExclusive("repo", "group")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	o.account = args[0]

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

	id, isID, err := serviceaccountutils.ParseID(o.account)
	if err != nil {
		return cmdutils.WrapError(err, "failed to get service account")
	}

	var raw any
	var account serviceaccountutils.Account
	switch {
	// The list item carries the same fields as the single-account endpoint,
	// which only exists from GitLab 19.4.
	case !isID:
		raw, account, err = serviceaccountutils.FindByUsername(ctx, client, scope, o.account)
		if err != nil {
			return cmdutils.WrapError(err, "failed to get service account")
		}
	case scope.Group != "":
		a, _, err := client.Groups.GetServiceAccount(scope.Group, id, gitlab.WithContext(ctx))
		if err != nil {
			return cmdutils.WrapError(err, "failed to get service account")
		}
		raw, account = a, serviceaccountutils.FromGroup(a)
	default:
		a, _, err := client.Projects.GetProjectServiceAccount(scope.Project, id, gitlab.WithContext(ctx))
		if err != nil {
			return cmdutils.WrapError(err, "failed to get service account")
		}
		raw, account = a, serviceaccountutils.FromProject(a)
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(raw)
	default:
		return o.printDetails(account)
	}
}

func (o *options) printDetails(a serviceaccountutils.Account) error {
	c := o.io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("ID"), a.ID)
	table.AddRow(c.Bold("Username"), a.Username)
	table.AddRow(c.Bold("Name"), serviceaccountutils.OrDash(a.Name))
	table.AddRow(c.Bold("Email"), serviceaccountutils.OrDash(a.Email))
	o.io.LogInfof("%s", table.Render())

	return nil
}
