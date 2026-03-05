package create

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/token/accesslevel"
	"gitlab.com/gitlab-org/cli/internal/commands/token/expirationdate"
	"gitlab.com/gitlab-org/cli/internal/commands/token/filter"
	"gitlab.com/gitlab-org/cli/internal/commands/token/tokenduration"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams
	baseRepo  func() (glrepo.Interface, error)

	name         string
	description  string
	user         string
	group        string
	accessLevel  accesslevel.AccessLevel
	scopes       []string
	duration     tokenduration.TokenDuration
	expireAt     expirationdate.ExpirationDate
	outputFormat string
}

func NewCmdCreate(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
		duration:  tokenduration.TokenDuration(30 * 24 * time.Hour), // Default: 30 days
	}

	cmd := &cobra.Command{
		Use:     "create <name>",
		Aliases: []string{"create", "new"},
		Args:    cobra.RangeArgs(1, 1),
		Short:   "Creates user, group, or project access tokens.",
		Long: heredoc.Doc(`
		Creates a new access token for a user, group, or project. Defaults to a
		project access token, unless user or group name is specified.

		The expiration date of the token is calculated by adding the duration
		(default: 30 days) to the current date, with expiration occurring at midnight
		UTC on the calculated date. You can specify a different duration using days (d),
		weeks (w), or hours (h), or provide an explicit end date.

		The name of the token must be unique. The token is printed to stdout.

		Administrators can create full-featured personal access tokens for themselves and for other users.

		Non-administrators can create personal access tokens only for
		themselves (@me). These tokens must use the scope 'k8s_proxy'. For more
		information, see the GitLab documentation for the
		[User tokens API](https://docs.gitlab.com/api/user_tokens/#create-a-personal-access-token).
		`),
		Example: heredoc.Doc(`
		# Create project access token for current project (default 30 days)
		$ glab token create --access-level developer --scope read_repository --scope read_registry my-project-token

		# Create project access token with 7 day lifetime
		$ glab token create --repo user/my-repo --access-level owner --scope api my-project-token --duration 7d

		# Create a group access token expiring in 2 weeks
		$ glab token create --group group/sub-group --access-level owner --scope api my-group-token --duration 2w

		# Create a personal access token for current user with 90 day lifetime
		$ glab token create --user @me --scope k8s_proxy my-personal-token --duration 90d

		# (administrator only) Create a personal access token for another user
		$ glab token create --user johndoe --scope api johns-personal-token --duration 180d

		# Create a token with explicit expiration date
		$ glab token create --access-level developer --scope api my-token --expires-at 2025-12-31

		`),
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}

			if err := opts.validate(cmd); err != nil {
				return err
			}

			return opts.run()
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)
	cmd.Flags().StringVarP(&opts.group, "group", "g", "", "Create a group access token. Ignored if a user or repository argument is set.")
	cmd.Flags().StringVarP(&opts.user, "user", "U", "", "Create a personal access token. For the current user, use @me.")
	cmd.Flags().StringVarP(&opts.description, "description", "", "", "Sets the token's description.")
	cmd.Flags().VarP(&opts.duration, "duration", "D", "Sets the token lifetime in days. Accepts: days (30d), weeks (4w), or hours in multiples of 24 (24h, 168h, 720h). Maximum: 365d. The token expires at midnight UTC on the calculated date.")
	cmd.Flags().VarP(&opts.expireAt, "expires-at", "E", "Sets the token's expiration date and time, in YYYY-MM-DD format. If not specified, --duration is used.")
	cmd.Flags().StringSliceVarP(&opts.scopes, "scope", "S", []string{"read_repository"}, "Scopes for the token. Multiple scopes can be comma-separated or specified by repeating the flag. For a list, see https://docs.gitlab.com/user/profile/personal_access_tokens/#personal-access-token-scopes.")
	cmd.Flags().VarP(&opts.accessLevel, "access-level", "A", "Access level of the token: one of 'guest', 'reporter', 'developer', 'maintainer', 'owner'.")
	cmdutils.EnableJSONOutput(cmd, &opts.outputFormat, "Format output as 'text' for the token value, 'json' for the actual API token structure.")
	cmd.MarkFlagsMutuallyExclusive("user", "group")
	cmd.MarkFlagsMutuallyExclusive("expires-at", "duration")
	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	o.name = args[0]

	if group, err := cmdutils.GroupOverride(cmd); err != nil {
		return err
	} else {
		o.group = group
	}

	if time.Time(o.expireAt).IsZero() {
		o.expireAt = expirationdate.ExpirationDate(o.duration.CalculateExpirationDate())
	}

	return nil
}

func (o *options) validate(cmd *cobra.Command) error {
	if o.user == "" && !cmd.Flag("access-level").Changed {
		return cmdutils.FlagError{Err: errors.New("the required flag '--access-level' is not set")}
	}

	return nil
}

func (o *options) run() error {
	// NOTE: this command can not only be used for projects,
	// so we have to manually check for the base repo, if it doesn't exist,
	// we bootstrap the client with the default hostname.
	var repoHost string
	if baseRepo, err := o.baseRepo(); err == nil {
		repoHost = baseRepo.RepoHost()
	}
	apiClient, err := o.apiClient(repoHost)
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	var outputToken any
	var outputTokenValue string
	expirationDate := gitlab.ISOTime(o.expireAt)

	if o.user != "" {
		user, err := api.UserByName(client, o.user)
		if err != nil {
			return err
		}

		options := &gitlab.ListPersonalAccessTokensOptions{
			ListOptions: gitlab.ListOptions{PerPage: 100},
			UserID:      &user.ID,
		}
		tokens, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.PersonalAccessToken, *gitlab.Response, error) {
			return client.PersonalAccessTokens.ListPersonalAccessTokens(options, p)
		})
		if err != nil {
			return err
		}
		tokens = filter.Filter(tokens, func(t *gitlab.PersonalAccessToken) bool {
			return t.Active && t.Name == o.name
		})
		if len(tokens) > 0 {
			return cmdutils.FlagError{Err: fmt.Errorf("a personal access token with the name %s already exists.", o.name)}
		}

		if o.user == "@me" {
			token, _, err := client.Users.CreatePersonalAccessTokenForCurrentUser(&gitlab.CreatePersonalAccessTokenForCurrentUserOptions{
				Name:      gitlab.Ptr(o.name),
				Scopes:    gitlab.Ptr(o.scopes),
				ExpiresAt: gitlab.Ptr(expirationDate),
			})
			if err != nil {
				return err
			}
			outputToken = token
			outputTokenValue = token.Token
		} else {
			createOptions := &gitlab.CreatePersonalAccessTokenOptions{
				Name:        &o.name,
				Description: &o.description,
				ExpiresAt:   &expirationDate,
				Scopes:      &o.scopes,
			}
			token, _, err := client.Users.CreatePersonalAccessToken(user.ID, createOptions)
			if err != nil {
				return err
			}
			outputToken = token
			outputTokenValue = token.Token
		}
	} else {
		if o.group != "" {
			listOptions := &gitlab.ListGroupAccessTokensOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}
			tokens, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.GroupAccessToken, *gitlab.Response, error) {
				return client.GroupAccessTokens.ListGroupAccessTokens(o.group, listOptions, p)
			})
			if err != nil {
				return err
			}
			tokens = filter.Filter(tokens, func(t *gitlab.GroupAccessToken) bool {
				return t.Active && t.Name == o.name
			})
			if len(tokens) > 0 {
				return cmdutils.FlagError{Err: fmt.Errorf("a group access token with the name %s already exists.", o.name)}
			}

			options := &gitlab.CreateGroupAccessTokenOptions{
				Name:        &o.name,
				Description: &o.description,
				Scopes:      &o.scopes,
				AccessLevel: &o.accessLevel.Value,
				ExpiresAt:   &expirationDate,
			}
			token, _, err := client.GroupAccessTokens.CreateGroupAccessToken(o.group, options)
			if err != nil {
				return err
			}
			outputToken = token
			outputTokenValue = token.Token

		} else {
			repo, err := o.baseRepo()
			if err != nil {
				return err
			}
			listOptions := &gitlab.ListProjectAccessTokensOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}
			tokens, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.ProjectAccessToken, *gitlab.Response, error) {
				return client.ProjectAccessTokens.ListProjectAccessTokens(repo.FullName(), listOptions, p)
			})
			if err != nil {
				return err
			}
			tokens = filter.Filter(tokens, func(t *gitlab.ProjectAccessToken) bool {
				return t.Active && t.Name == o.name
			})

			if len(tokens) > 0 {
				return cmdutils.FlagError{Err: fmt.Errorf("a project access token with name %s already exists.", o.name)}
			}

			options := &gitlab.CreateProjectAccessTokenOptions{
				Name:        &o.name,
				Description: &o.description,
				Scopes:      &o.scopes,
				AccessLevel: &o.accessLevel.Value,
				ExpiresAt:   &expirationDate,
			}
			token, _, err := client.ProjectAccessTokens.CreateProjectAccessToken(repo.FullName(), options)
			if err != nil {
				return err
			}
			outputToken = token
			outputTokenValue = token.Token
		}
	}

	if o.outputFormat == "json" {
		encoder := json.NewEncoder(o.io.StdOut)
		encoder.SetIndent("  ", "  ")
		if err := encoder.Encode(outputToken); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(o.io.StdOut, "%s\n", outputTokenValue); err != nil {
			return err
		}
	}

	return nil
}
