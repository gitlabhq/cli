package revoke

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
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
	baseRepo  func() (glrepo.Interface, error)

	account string
	token   string
	group   string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:               "revoke <account-id | username> <token-id | name> [flags]",
		Short:             "Revoke a token of a service account.",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Docf(`
			Revoke a personal access token of a service account of a project or a group.

			- Anything authenticating with the token stops working immediately.
			- A token name matches active tokens only. If several active tokens share the name, use the token ID instead.
			- Numeric arguments are always treated as IDs. Token IDs and names come from %[1]sglab service-account token list%[1]s.

			Revoking a token of a group service account requires GitLab 17.11 or later.
		`, "`"),
		Example: heredoc.Doc(`
			# Revoke token 7 of service account 42 of the current project
			glab service-account token revoke 42 7

			# Revoke a token by its name, finding the account by its username
			glab service-account token revoke release-bot ci

			# Revoke a token of a group's service account
			glab service-account token revoke 42 7 --group my-group`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
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
	fl.StringVarP(&opts.group, "group", "g", "", "Revoke a token of a service account of a `group` instead of a project. Accepts a group ID or full path.")

	cmd.MarkFlagsMutuallyExclusive("repo", "group")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	o.account = args[0]
	o.token = args[1]

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
		return cmdutils.WrapError(err, "failed to revoke service account token")
	}

	tokenID, err := serviceaccountutils.ResolveTokenID(ctx, client, scope, accountID, o.token)
	if err != nil {
		return cmdutils.WrapError(err, "failed to revoke service account token")
	}

	if scope.Group != "" {
		_, err = client.Groups.RevokeServiceAccountPersonalAccessToken(scope.Group, accountID, tokenID, gitlab.WithContext(ctx))
	} else {
		_, err = client.Projects.RevokeProjectServiceAccountPersonalAccessToken(scope.Project, accountID, tokenID, gitlab.WithContext(ctx))
	}
	if err != nil {
		return cmdutils.WrapError(err, "failed to revoke service account token")
	}

	o.io.LogInfof("%s Revoked token %d of service account %d in %s.\n", o.io.Color().RedCheck(), tokenID, accountID, scope)

	return nil
}
