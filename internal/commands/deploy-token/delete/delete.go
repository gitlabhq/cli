package delete

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
	baseRepo  func() (glrepo.Interface, error)

	tokenID int64
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
		Use:               "delete <token-id> [flags]",
		Short:             "Delete a deploy token.",
		Aliases:           []string{"del"},
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Docf(`
			Delete a deploy token of a project or a group, by its ID.

			The deletion is permanent and takes effect immediately. Anything authenticating with the token stops working.

			Deleting is not the revoke action from the GitLab UI, which the REST API does not expose. A revoked token stays listed and is reported as revoked, while a deleted token is gone from %[1]sglab deploy-token list%[1]s entirely.

			Token IDs come from %[1]sglab deploy-token list%[1]s.
		`, "`"),
		Example: heredoc.Doc(`
			# Delete deploy token 42 of the current project, after confirming
			glab deploy-token delete 42

			# Delete it without a confirmation prompt
			glab deploy-token delete 42 --yes

			# Delete a deploy token of a specific project
			glab deploy-token delete 42 --repo user/my-repo

			# Delete a group's deploy token
			glab deploy-token delete 42 --group group/sub-group`),
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
	fl.StringVarP(&opts.group, "group", "g", "", "Delete a deploy token of a `group` instead of a project. Accepts a group ID or full path.")
	fl.BoolVarP(&opts.yes, "yes", "y", false, "Skip the confirmation prompt.")

	cmd.MarkFlagsMutuallyExclusive("repo", "group")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	tokenID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		// ExitError renders only the wrapped error, so the rejected value has to
		// be named here. It must not lead the message: the error handler
		// capitalizes the first character, which would rewrite the value itself.
		return cmdutils.WrapError(
			fmt.Errorf("invalid deploy token ID: %q", args[0]),
			"invalid deploy token ID",
		)
	}
	o.tokenID = tokenID

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
	// The group scope works outside a repository, so a missing base repo is only
	// fatal for the project scope. Fail before building a client that would be
	// discarded, so the caller sees why the repository was needed.
	repo, repoErr := o.baseRepo()
	if o.group == "" && repoErr != nil {
		return repoErr
	}

	var repoHost string
	if repoErr == nil {
		repoHost = repo.RepoHost()
	}

	apiClient, err := o.apiClient(repoHost)
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	// Naming the target is what makes a GITLAB_GROUP redirect visible before
	// anything is destroyed. The group branch comes first because repo is nil
	// whenever a group was given outside a repository.
	var target string
	switch {
	case o.group != "":
		target = "group " + o.group
	default:
		target = "project " + repo.FullName()
	}

	if err := o.confirmDeletion(ctx, target); err != nil {
		return err
	}

	switch {
	case o.group != "":
		_, err = client.DeployTokens.DeleteGroupDeployToken(o.group, o.tokenID, gitlab.WithContext(ctx))
	default:
		_, err = client.DeployTokens.DeleteProjectDeployToken(repo.FullName(), o.tokenID, gitlab.WithContext(ctx))
	}
	if err != nil {
		return cmdutils.WrapError(err, "failed to delete deploy token")
	}

	c := o.io.Color()
	o.io.LogInfof("%s Deleted deploy token %d of %s.\n", c.RedCheck(), o.tokenID, target)

	return nil
}

func (o *options) confirmDeletion(ctx context.Context, target string) error {
	if o.yes {
		return nil
	}

	o.io.LogErrorf("This permanently deletes deploy token %d of %s. Anything authenticating with it stops working.\n\n", o.tokenID, target)

	var confirmed bool
	if err := o.io.Confirm(ctx, &confirmed, fmt.Sprintf("Are you ABSOLUTELY SURE you wish to delete deploy token %d?", o.tokenID)); err != nil {
		return cmdutils.WrapError(err, "could not prompt")
	}
	if !confirmed {
		return cmdutils.CancelError()
	}

	return nil
}
