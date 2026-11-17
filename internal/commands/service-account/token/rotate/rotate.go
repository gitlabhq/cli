package rotate

import (
	"context"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/cmdutils/tokenduration"
	"gitlab.com/gitlab-org/cli/internal/commands/service-account/serviceaccountutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
	baseRepo  func() (glrepo.Interface, error)

	account      string
	token        string
	duration     tokenduration.TokenDuration
	expiresAt    time.Time
	expiresAtSet bool
	group        string
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
		duration:  tokenduration.TokenDuration(30 * 24 * time.Hour),
	}

	cmd := &cobra.Command{
		Use:               "rotate <account-id | username> <token-id | name> [flags]",
		Short:             "Rotate a token of a service account.",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Docf(`
			Rotate a personal access token of a service account of a project or a group.

			- Rotating revokes the token and creates a new one with the same name, description, and scopes. Anything using the old value stops working immediately.
			- The new token value is printed only once and cannot be retrieved later. It is the only output on stdout, and the token details go to stderr.
			- The new token expires after %[1]s--duration%[1]s, 30 days by default, at 00:00 UTC. Use %[1]s--expires-at%[1]s for a specific date instead.
			- A token name matches active tokens only. If several active tokens share the name, use the token ID instead.
			- Numeric arguments are always treated as IDs. Token IDs and names come from %[1]sglab service-account token list%[1]s.

			Finding a token by name for a group service account requires GitLab 17.11 or later.
		`, "`"),
		Example: heredoc.Doc(`
			# Rotate token 7 of service account 42 of the current project
			glab service-account token rotate 42 7

			# Rotate a token by its name for 90 days, finding the account by its username
			glab service-account token rotate release-bot ci --duration 90d

			# Rotate a token of a group's service account until the end of 2027
			glab service-account token rotate 42 7 --expires-at 2027-12-31 --group my-group

			# Rotate a token and store the new value in a variable
			TOKEN=$(glab service-account token rotate 42 ci)`),
		Annotations: map[string]string{
			// Rotating a token prints a secret, so this command is kept out of MCP,
			// as glab token rotate is.
			mcpannotations.Exclude: "true",
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
	fl.StringVarP(&opts.group, "group", "g", "", "Rotate a token of a service account of a `group` instead of a project. Accepts a group ID or full path.")
	fl.VarP(&opts.duration, "duration", "D", "Sets the token lifetime in days. Accepts: days (30d), weeks (4w), or hours in multiples of 24 (24h, 168h, 720h). Maximum: 365d. The token expires at midnight UTC on the calculated date.")
	fl.TimeVarP(&opts.expiresAt, "expires-at", "E", time.Time{}, []string{time.DateOnly}, "Expiry date of the new token, as YYYY-MM-DD. The token expires at midnight UTC on that date.")

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat, "Format output as 'text' for the token value, 'json' for the token with its details.")
	cmd.MarkFlagsMutuallyExclusive("duration", "expires-at")
	cmd.MarkFlagsMutuallyExclusive("repo", "group")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	o.account = args[0]
	o.token = args[1]
	o.expiresAtSet = cmd.Flags().Changed("expires-at")

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
		return cmdutils.WrapError(err, "failed to rotate service account token")
	}

	tokenID, err := serviceaccountutils.ResolveTokenID(ctx, client, scope, accountID, o.token)
	if err != nil {
		return cmdutils.WrapError(err, "failed to rotate service account token")
	}

	expiry := o.duration.CalculateExpirationDate()
	if o.expiresAtSet {
		expiry = o.expiresAt
	}
	expiresAt := new(gitlab.ISOTime(expiry))

	var token *gitlab.PersonalAccessToken
	if scope.Group != "" {
		token, _, err = client.Groups.RotateServiceAccountPersonalAccessToken(scope.Group, accountID, tokenID, &gitlab.RotateServiceAccountPersonalAccessTokenOptions{
			ExpiresAt: expiresAt,
		}, gitlab.WithContext(ctx))
	} else {
		token, _, err = client.Projects.RotateProjectServiceAccountPersonalAccessToken(scope.Project, accountID, tokenID, &gitlab.RotateProjectServiceAccountPersonalAccessTokenOptions{
			ExpiresAt: expiresAt,
		}, gitlab.WithContext(ctx))
	}
	if err != nil {
		return cmdutils.WrapError(err, "failed to rotate service account token")
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(token)
	}

	if err := serviceaccountutils.PrintToken(o.io, token); err != nil {
		return err
	}
	o.io.LogErrorf("%s Rotated token %d of service account %d in %s into token %d. Store the token value now. It is not shown again.\n", o.io.Color().WarnIcon(), tokenID, accountID, scope, token.ID)

	return nil
}
