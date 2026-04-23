package revoke

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/token/filter"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	tokenID int64
	name    string

	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams
	baseRepo  func() (glrepo.Interface, error)

	user         string
	group        string
	outputFormat string
}

func NewCmdRevoke(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:     "revoke <token-name|token-id>",
		Short:   "Revoke user, group, or project access tokens.",
		Aliases: []string{"revoke", "rm"},
		Args:    cobra.RangeArgs(1, 1),
		Long: heredoc.Doc(`
			Revoke an user, group or project access token. If multiple tokens with the same name exist, you can specify
			the ID of the token.

			The output format can be either "JSON" or "text". The JSON output will show the meta information of the
			revoked token. The normal text output is a description of the revoked token name and ID.

			Administrators can revoke personal access tokens belonging to other users.
		`),

		Example: heredoc.Doc(`
		# Revoke a project access token of current project
		glab token revoke my-project-token

		# Revoke a project access token of a specific project
		glab token revoke --repo user/my-repo my-project-token

		# Revoke a group access token
		glab token revoke --group group/sub-group my-group-token

		# Revoke my personal access token
		glab token revoke --user @me my-personal-token

		# Revoke a personal access token of another user (administrator only)
		glab token revoke --user johndoe johns-personal-token`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}

			return opts.run()
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)
	cmd.Flags().StringVarP(&opts.group, "group", "g", "", "Revoke group access token. Ignored if a user or repository argument is set.")
	cmd.Flags().StringVarP(&opts.user, "user", "U", "", "Revoke personal access token. Use @me for the current user.")
	cmdutils.EnableJSONOutput(cmd, &opts.outputFormat, "Format output as: text, json. 'text' provides the name and ID of the revoked token; 'json' outputs the token with metadata.")
	cmd.MarkFlagsMutuallyExclusive("group", "user")
	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	if tokenID, err := strconv.ParseInt(args[0], 10, 64); err != nil {
		o.name = args[0]
	} else {
		o.tokenID = tokenID
	}
	if group, err := cmdutils.GroupOverride(cmd); err != nil {
		return err
	} else {
		o.group = group
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
			return t.Active && (t.Name == o.name || t.ID == o.tokenID)
		})
		switch len(tokens) {
		case 1:
			token = tokens[0]
		case 0:
			return cmdutils.FlagError{Err: fmt.Errorf("no token found with the name '%v'.", o.name)}
		default:
			return cmdutils.FlagError{Err: fmt.Errorf("multiple tokens found with the name '%v'. Use the ID instead.", o.name)}
		}
		if _, err = client.PersonalAccessTokens.RevokePersonalAccessTokenByID(token.ID); err != nil {
			return err
		}
		token.Revoked = true
		outputToken = token
		outputTokenValue = fmt.Sprintf("revoked %s %s %d", o.user, token.Name, token.ID)
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
				return t.Active && (t.Name == o.name || t.ID == o.tokenID)
			})
			switch len(tokens) {
			case 1:
				token = tokens[0]
			case 0:
				return cmdutils.FlagError{Err: fmt.Errorf("no token found with the name '%v'.", o.name)}
			default:
				return cmdutils.FlagError{Err: fmt.Errorf("multiple tokens found with the name '%v'. Use the ID instead.", o.name)}
			}

			if _, err = client.GroupAccessTokens.RevokeGroupAccessToken(o.group, token.ID); err != nil {
				return err
			}
			token.Revoked = true
			outputToken = token
			outputTokenValue = fmt.Sprintf("revoked %s %d", token.Name, token.ID)
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
				return t.Active && (t.Name == o.name || t.ID == o.tokenID)
			})
			var token *gitlab.ProjectAccessToken
			switch len(tokens) {
			case 1:
				token = tokens[0]
			case 0:
				return cmdutils.FlagError{Err: fmt.Errorf("no token found with the name '%v'.", o.name)}
			default:
				return cmdutils.FlagError{Err: fmt.Errorf("multiple tokens found with the name '%v'. Use the ID instead.", o.name)}
			}

			if _, err := client.ProjectAccessTokens.RevokeProjectAccessToken(repo.FullName(), token.ID); err != nil {
				return err
			}
			token.Revoked = true
			outputToken = token
			outputTokenValue = fmt.Sprintf("revoked %s %d", token.Name, token.ID)
		}
	}

	if o.outputFormat == "json" {
		encoder := json.NewEncoder(o.io.StdOut)
		if err := encoder.Encode(outputToken); err != nil {
			return err
		}
	} else {
		if _, err := o.io.StdOut.Write([]byte(outputTokenValue)); err != nil {
			return err
		}
	}

	return nil
}
