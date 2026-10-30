package sshkeyutils

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/dbg"
)

func SSHKeyIDCompletions(ctx context.Context, f cmdutils.Factory) ([]cobra.Completion, cobra.ShellCompDirective) {
	apiClient, err := f.ApiClient("")
	if err != nil {
		dbg.Debugf("error obtaining API client: %v", err)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	opts := &gitlab.ListSSHKeysOptions{ListOptions: gitlab.ListOptions{PerPage: api.MaxPerPage}}
	keys, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.SSHKey, *gitlab.Response, error) {
		return apiClient.Lab().Users.ListSSHKeys(opts, p, gitlab.WithContext(ctx))
	})
	if err != nil {
		dbg.Debugf("error collecting SSH keys: %v", err)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	completions := make([]cobra.Completion, 0, len(keys))
	for _, key := range keys {
		completions = append(completions, fmt.Sprintf("%d\t%s", key.ID, key.Title))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
