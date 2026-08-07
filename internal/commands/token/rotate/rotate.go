package rotate

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
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

	user         string
	group        string
	name         any
	duration     tokenduration.TokenDuration
	expireAt     expirationdate.ExpirationDate
	outputFormat string
}

func NewCmdRotate(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
		duration:  tokenduration.TokenDuration(30 * 24 * time.Hour), // Default: 30 days
	}

	cmd := &cobra.Command{
		Use:     "rotate <token-name|token-id>",
		Short:   "Rotate user, group, or project access tokens.",
		Aliases: []string{"rot"},
		Args:    cobra.ExactArgs(1),
		Long: heredoc.Docf(`
			Rotating a token revokes the existing token and creates a new one
			with a new secret value and expiration date.
			The old token stops working immediately, so you must update any client that uses
			it with the new value that this command returns.

			If multiple tokens share the same name, specify the token ID to select the correct one.

			The token expires at 00:00 UTC on a date calculated by adding the duration to today's date.
			The default duration is 30 days. You can specify a different duration in days (%[1]sd%[1]s),
			weeks (%[1]sw%[1]s), or hours (%[1]sh%[1]s).
			The %[1]s--duration%[1]s and %[1]s--expires-at%[1]s flags are mutually exclusive.

			Administrators can rotate personal access tokens that belong to other users.
		`, "`"),
		Example: heredoc.Doc(`
		# Rotate project access token of current project (default 30 days)
		glab token rotate my-project-token

		# Rotate project access token with explicit expiration date
		glab token rotate --repo user/repo my-project-token --expires-at 2025-08-08

		# Rotate group access token with 7 day lifetime
		glab token rotate --group group/sub-group my-group-token --duration 7d

		# Rotate personal access token with 2 week lifetime
		glab token rotate --user @me my-personal-token --duration 2w

		# Rotate a personal access token of another user (administrator only)
		glab token rotate --user johndoe johns-personal-token --duration 90d`),
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}

			if err := opts.validate(); err != nil {
				return err
			}

			return opts.run()
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)
	fl := cmd.Flags()
	fl.StringVarP(&opts.group, "group", "g", "", "Rotate group access token. Ignored if a user or repository argument is set.")
	fl.StringVarP(&opts.user, "user", "U", "", "Rotate personal access token. Use @me for the current user.")
	fl.VarP(&opts.duration, "duration", "D", "Sets the token lifetime in days. Accepts: days (30d), weeks (4w), or hours in multiples of 24 (24h, 168h, 720h). Maximum: 365d. The token expires at 00:00 UTC on the calculated date.")
	fl.VarP(&opts.expireAt, "expires-at", "E", "Sets the token's expiration date and time, in YYYY-MM-DD format. If not specified, --duration is used.")
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat, "Format output as: text, json. 'text' provides the new token value; 'json' outputs the token with metadata.")
	cmd.MarkFlagsMutuallyExclusive("duration", "expires-at")
	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	if name, err := strconv.ParseInt(args[0], 10, 64); err != nil {
		o.name = args[0]
	} else {
		o.name = name
	}

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

func (o *options) nameKind() string {
	if _, ok := o.name.(int64); ok {
		return "ID"
	}
	return "name"
}

func (o *options) validate() error {
	if o.group != "" && o.user != "" {
		return cmdutils.FlagError{Err: errors.New("'--group' and '--user' are mutually exclusive")}
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

	expirationDate := gitlab.ISOTime(o.expireAt)

	var outputToken any
	var outputTokenValue string

	if o.user != "" {
		user, err := api.UserByName(client, o.user)
		if err != nil {
			return cmdutils.FlagError{Err: err}
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
		var token *gitlab.PersonalAccessToken
		tokens = filter.Filter(tokens, func(t *gitlab.PersonalAccessToken) bool {
			return t.Active && (t.Name == o.name || t.ID == o.name)
		})
		switch len(tokens) {
		case 1:
			token = tokens[0]
		case 0:
			return cmdutils.FlagError{Err: fmt.Errorf("no active token found with the %s '%v'", o.nameKind(), o.name)}
		default:
			return cmdutils.FlagError{Err: fmt.Errorf("multiple tokens found with the name %q; use the ID instead", o.name)}
		}
		rotateOptions := &gitlab.RotatePersonalAccessTokenOptions{
			ExpiresAt: &expirationDate,
		}
		if token, _, err = client.PersonalAccessTokens.RotatePersonalAccessToken(token.ID, rotateOptions); err != nil {
			return err
		}
		outputToken = token
		outputTokenValue = token.Token
	} else {
		if o.group != "" {
			options := &gitlab.ListGroupAccessTokensOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}
			tokens, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.GroupAccessToken, *gitlab.Response, error) {
				return client.GroupAccessTokens.ListGroupAccessTokens(o.group, options, p)
			})
			if err != nil {
				return err
			}
			var token *gitlab.GroupAccessToken
			tokens = filter.Filter(tokens, func(t *gitlab.GroupAccessToken) bool {
				return t.Active && (t.Name == o.name || t.ID == o.name)
			})
			switch len(tokens) {
			case 1:
				token = tokens[0]
			case 0:
				return cmdutils.FlagError{Err: fmt.Errorf("no active token found with the %s '%v'", o.nameKind(), o.name)}
			default:
				return cmdutils.FlagError{Err: fmt.Errorf("multiple tokens found with the name '%v', use the ID instead", o.name)}
			}

			rotateOptions := &gitlab.RotateGroupAccessTokenOptions{
				ExpiresAt: &expirationDate,
			}
			if token, _, err = client.GroupAccessTokens.RotateGroupAccessToken(o.group, token.ID, rotateOptions); err != nil {
				return err
			}
			outputToken = token
			outputTokenValue = token.Token
		} else {
			repo, err := o.baseRepo()
			if err != nil {
				return err
			}
			options := &gitlab.ListProjectAccessTokensOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}
			tokens, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.ProjectAccessToken, *gitlab.Response, error) {
				return client.ProjectAccessTokens.ListProjectAccessTokens(repo.FullName(), options, p)
			})
			if err != nil {
				return err
			}
			tokens = filter.Filter(tokens, func(t *gitlab.ProjectAccessToken) bool {
				return t.Active && (t.Name == o.name || t.ID == o.name)
			})
			var token *gitlab.ProjectAccessToken
			switch len(tokens) {
			case 1:
				token = tokens[0]
			case 0:
				return cmdutils.FlagError{Err: fmt.Errorf("no active token found with the %s '%v'", o.nameKind(), o.name)}
			default:
				return cmdutils.FlagError{Err: fmt.Errorf("multiple tokens found with the name '%v', use the ID instead", o.name)}
			}

			rotateOptions := &gitlab.RotateProjectAccessTokenOptions{
				ExpiresAt: &expirationDate,
			}
			if token, _, err = client.ProjectAccessTokens.RotateProjectAccessToken(repo.FullName(), token.ID, rotateOptions); err != nil {
				return err
			}
			outputToken = token
			outputTokenValue = token.Token
		}
	}

	if o.outputFormat == "json" {
		if err := o.io.PrintJSON(outputToken); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(o.io.StdOut, outputTokenValue); err != nil { //nolint:forbidigo // write error must propagate to the caller
		return err
	}

	return nil
}
