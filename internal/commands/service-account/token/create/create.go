package create

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

	account        string
	name           string
	scopes         []string
	description    string
	descriptionSet bool
	duration       tokenduration.TokenDuration
	expiresAt      time.Time
	expiresAtSet   bool
	group          string
	outputFormat   string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
		duration:  tokenduration.TokenDuration(30 * 24 * time.Hour),
	}

	cmd := &cobra.Command{
		Use:               "create <account-id | username> <name> [flags]",
		Short:             "Create a token for a service account.",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Docf(`
			Create a personal access token for a service account of a project or a group.

			- The token value is printed only once and cannot be retrieved later. It is the only output on stdout, and the token details go to stderr.
			- At least one scope is required. For the scopes you can grant, see https://docs.gitlab.com/security/tokens/access_token_scopes/.
			- The token expires after %[1]s--duration%[1]s, 30 days by default, at 00:00 UTC. Use %[1]s--expires-at%[1]s for a specific date instead.
			- A numeric account argument is always treated as an ID.
		`, "`"),
		Example: heredoc.Doc(`
			# Create a token for service account 42 of the current project
			glab service-account token create 42 ci --scope api

			# Create a read-only token that expires in a week, finding the account by its username
			glab service-account token create release-bot ci-reader --scope read_api,read_repository --duration 7d

			# Create a token for a group's service account that expires at the end of 2027
			glab service-account token create 42 deploy --scope api --expires-at 2027-12-31 --group my-group

			# Create a token and store its value in a variable
			TOKEN=$(glab service-account token create 42 ci --scope api)`),
		Annotations: map[string]string{
			// Creating a token prints a secret, so this command is kept out of MCP,
			// as glab token create is.
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
	fl.StringVarP(&opts.group, "group", "g", "", "Create a token for a service account of a `group` instead of a project. Accepts a group ID or full path.")
	fl.StringSliceVarP(&opts.scopes, "scope", "S", nil, "Scope to grant the token. Multiple scopes can be comma-separated or specified by repeating the flag.")
	fl.StringVar(&opts.description, "description", "", "Description of the token.")
	fl.VarP(&opts.duration, "duration", "D", "Sets the token lifetime in days. Accepts: days (30d), weeks (4w), or hours in multiples of 24 (24h, 168h, 720h). Maximum: 365d. The token expires at midnight UTC on the calculated date.")
	fl.TimeVarP(&opts.expiresAt, "expires-at", "E", time.Time{}, []string{time.DateOnly}, "Expiry date of the token, as YYYY-MM-DD. The token expires at midnight UTC on that date.")

	cobra.CheckErr(cmd.MarkFlagRequired("scope"))

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat, "Format output as 'text' for the token value, 'json' for the token with its details.")
	cmd.MarkFlagsMutuallyExclusive("duration", "expires-at")
	cmd.MarkFlagsMutuallyExclusive("repo", "group")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	o.account = args[0]
	o.name = args[1]

	fl := cmd.Flags()
	o.descriptionSet = fl.Changed("description")
	o.expiresAtSet = fl.Changed("expires-at")

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
		return cmdutils.WrapError(err, "failed to create service account token")
	}

	expiry := o.duration.CalculateExpirationDate()
	if o.expiresAtSet {
		expiry = o.expiresAt
	}
	expiresAt := new(gitlab.ISOTime(expiry))

	var description *string
	if o.descriptionSet {
		description = &o.description
	}

	var token *gitlab.PersonalAccessToken
	if scope.Group != "" {
		token, _, err = client.Groups.CreateServiceAccountPersonalAccessToken(scope.Group, accountID, &gitlab.CreateServiceAccountPersonalAccessTokenOptions{
			Name:        &o.name,
			Description: description,
			Scopes:      &o.scopes,
			ExpiresAt:   expiresAt,
		}, gitlab.WithContext(ctx))
	} else {
		token, _, err = client.Projects.CreateProjectServiceAccountPersonalAccessToken(scope.Project, accountID, &gitlab.CreateProjectServiceAccountPersonalAccessTokenOptions{
			Name:        &o.name,
			Description: description,
			Scopes:      &o.scopes,
			ExpiresAt:   expiresAt,
		}, gitlab.WithContext(ctx))
	}
	if err != nil {
		return cmdutils.WrapError(err, "failed to create service account token")
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(token)
	}

	if err := serviceaccountutils.PrintToken(o.io, token); err != nil {
		return err
	}
	o.io.LogErrorf("%s Created token %d for service account %d in %s. Store the token value now. It is not shown again.\n", o.io.Color().WarnIcon(), token.ID, accountID, scope)

	return nil
}
