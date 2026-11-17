package list

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
	active       bool
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
		Use:               "list <account-id | username> [flags]",
		Short:             "List the tokens of a service account.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Doc(`
			List the personal access tokens of a service account of a project or a group.

			- A numeric account argument is always treated as an ID.
			- Token values are never shown. GitLab returns a token's value only when the token is created or rotated.

			Listing the tokens of a group service account requires GitLab 17.11 or later.
		`),
		Example: heredoc.Doc(`
			# List the tokens of service account 42 of the current project
			glab service-account token list 42

			# List only the active tokens, finding the account by its username
			glab service-account token list release-bot --active

			# List the tokens of a group's service account
			glab service-account token list 42 --group my-group

			# Print the names of the active tokens
			glab service-account token list 42 --active --output json --jq '.[].name'`),
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
	fl.StringVarP(&opts.group, "group", "g", "", "List the tokens of a service account of a `group` instead of a project. Accepts a group ID or full path.")
	fl.BoolVarP(&opts.active, "active", "a", false, "List only the active tokens.")

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

	accountID, err := serviceaccountutils.ResolveID(ctx, client, scope, o.account)
	if err != nil {
		return cmdutils.WrapError(err, "failed to list service account tokens")
	}

	tokens, err := serviceaccountutils.ListTokens(ctx, client, scope, accountID, o.active)
	if err != nil {
		return cmdutils.WrapError(err, "failed to list service account tokens")
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(tokens)
	}

	o.printTable(tokens, accountID)
	return nil
}

func (o *options) printTable(tokens []*gitlab.PersonalAccessToken, accountID int64) {
	if len(tokens) == 0 {
		o.io.LogInfof("No tokens found for service account %d.\n", accountID)
		return
	}

	c := o.io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("ID"), c.Bold("Name"), c.Bold("Scopes"), c.Bold("Active"), c.Bold("Expires At"), c.Bold("Last Used At"))
	for _, t := range tokens {
		table.AddRow(
			t.ID,
			t.Name,
			serviceaccountutils.FormatScopes(t.Scopes),
			t.Active,
			serviceaccountutils.FormatDate(t.ExpiresAt),
			serviceaccountutils.FormatTime(t.LastUsedAt),
		)
	}
	o.io.LogInfof("%s", table.Render())
}
