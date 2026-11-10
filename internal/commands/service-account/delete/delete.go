package delete

import (
	"context"
	"errors"
	"fmt"

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
	group   string
	yes     bool
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:               "delete <id | username> [flags]",
		Short:             "Delete a service account.",
		Aliases:           []string{"del"},
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Docf(`
			Delete a service account of a project or a group, by its ID or username.

			The account's contributions, such as merge requests and comments, are moved to the ghost user. Anything authenticating with the account's tokens stops working.

			GitLab deletes the account in the background, so it can still appear in %[1]sglab service-account list%[1]s for a short time.

			A numeric argument is always treated as an ID. IDs and usernames come from %[1]sglab service-account list%[1]s.
		`, "`"),
		Example: heredoc.Doc(`
			# Delete service account 42 of the current project, after confirming
			glab service-account delete 42

			# Delete it without a confirmation prompt
			glab service-account delete 42 --yes

			# Delete a service account by its username
			glab service-account delete release-bot

			# Delete a service account of a specific project
			glab service-account delete 42 --repo user/my-repo

			# Delete a group's service account
			glab service-account delete 42 --group my-group`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)

	fl := cmd.Flags()
	fl.StringVarP(&opts.group, "group", "g", "", "Delete a service account of a `group` instead of a project. Accepts a group ID or full path.")
	fl.BoolVarP(&opts.yes, "yes", "y", false, "Skip the confirmation prompt.")

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

func (o *options) validate() error {
	if !o.yes && !o.io.PromptEnabled() {
		return &cmdutils.FlagError{Err: errors.New("--yes required when not running interactively")}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	client, scope, err := serviceaccountutils.NewClient(o.baseRepo, o.apiClient, o.group)
	if err != nil {
		return err
	}

	// Resolved before the prompt, so the prompt names the account by ID.
	id, err := serviceaccountutils.ResolveID(ctx, client, scope, o.account)
	if err != nil {
		return cmdutils.WrapError(err, "failed to delete service account")
	}

	if err := o.confirmDeletion(ctx, id, scope); err != nil {
		return err
	}

	// hard_delete stays unset: a soft delete keeps the account's contributions.
	if scope.Group != "" {
		_, err = client.Groups.DeleteServiceAccount(scope.Group, id, &gitlab.DeleteServiceAccountOptions{}, gitlab.WithContext(ctx))
	} else {
		_, err = client.Projects.DeleteProjectServiceAccount(scope.Project, id, &gitlab.DeleteProjectServiceAccountOptions{}, gitlab.WithContext(ctx))
	}
	if err != nil {
		return cmdutils.WrapError(err, "failed to delete service account")
	}

	o.io.LogInfof("%s Scheduled service account %d of %s for deletion.\n", o.io.Color().RedCheck(), id, scope)

	return nil
}

func (o *options) confirmDeletion(ctx context.Context, id int64, scope serviceaccountutils.Scope) error {
	if o.yes {
		return nil
	}

	o.io.LogErrorf("This deletes service account %d of %s. Its contributions move to the ghost user, and anything authenticating with its tokens stops working.\n\n", id, scope)

	var confirmed bool
	if err := o.io.Confirm(ctx, &confirmed, fmt.Sprintf("Are you ABSOLUTELY SURE you wish to delete service account %d?", id)); err != nil {
		return cmdutils.WrapError(err, "could not prompt")
	}
	if !confirmed {
		return cmdutils.CancelError()
	}

	return nil
}
