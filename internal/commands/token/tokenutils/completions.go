package tokenutils

import (
	"context"
	"strconv"

	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/dbg"
)

func GroupCompletions(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return cobra.NoFileCompletions(cmd, args, toComplete)
}

func UserCompletions(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return []cobra.Completion{cobra.CompletionWithDesc("@me", "Current user")}, cobra.ShellCompDirectiveNoFileComp
}

func NameOrIDCompletions(ctx context.Context, f cmdutils.Factory, cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	user, err := cmd.Flags().GetString("user")
	if err != nil {
		dbg.Debugf("error getting 'user' flag: %v", err)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	if err := cmdutils.ApplyRepoOverride(cmd, f); err != nil {
		dbg.Debugf("error applying repo override: %v", err)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		dbg.Debugf("error getting group: %v", err)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// NOTE: like the token commands themselves, this completion also works
	// outside of a repository, so we fall back to the default hostname.
	repoHost := ""
	if repo, err := f.BaseRepo(); err == nil {
		repoHost = repo.RepoHost()
	}
	apiClient, err := f.ApiClient(repoHost)
	if err != nil {
		dbg.Debugf("error obtaining API client: %v", err)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	client := apiClient.Lab()

	completions := []cobra.Completion{}

	switch {
	case user != "":
		u, err := api.UserByName(client, user)
		if err != nil {
			dbg.Debugf("error finding user %q: %v", user, err)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		options := &gitlab.ListPersonalAccessTokensOptions{
			PerPage: api.MaxPerPage,
			UserID:  &u.ID,
		}
		tokens, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.PersonalAccessToken, *gitlab.Response, error) {
			return client.PersonalAccessTokens.ListPersonalAccessTokens(options, p, gitlab.WithContext(ctx))
		})
		if err != nil {
			dbg.Debugf("error collecting personal access tokens: %v", err)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		for _, token := range tokens {
			completions = appendTokenCompletions(completions, token.Active, token.ID, token.Name)
		}
	case group != "":
		options := &gitlab.ListGroupAccessTokensOptions{ListOptions: gitlab.ListOptions{PerPage: api.MaxPerPage}}
		tokens, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.GroupAccessToken, *gitlab.Response, error) {
			return client.GroupAccessTokens.ListGroupAccessTokens(group, options, p, gitlab.WithContext(ctx))
		})
		if err != nil {
			dbg.Debugf("error collecting group access tokens: %v", err)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		for _, token := range tokens {
			completions = appendTokenCompletions(completions, token.Active, token.ID, token.Name)
		}
	default:
		repo, err := f.BaseRepo()
		if err != nil {
			dbg.Debugf("error resolving base repository: %v", err)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		options := &gitlab.ListProjectAccessTokensOptions{ListOptions: gitlab.ListOptions{PerPage: api.MaxPerPage}}
		tokens, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.ProjectAccessToken, *gitlab.Response, error) {
			return client.ProjectAccessTokens.ListProjectAccessTokens(repo.FullName(), options, p, gitlab.WithContext(ctx))
		})
		if err != nil {
			dbg.Debugf("error collecting project access tokens: %v", err)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		for _, token := range tokens {
			completions = appendTokenCompletions(completions, token.Active, token.ID, token.Name)
		}
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

func appendTokenCompletions(completions []cobra.Completion, active bool, id int64, name string) []cobra.Completion {
	if !active {
		return completions
	}
	strID := strconv.FormatInt(id, 10)
	return append(completions,
		cobra.CompletionWithDesc(strID, name),
		cobra.CompletionWithDesc(name, strID),
	)
}
